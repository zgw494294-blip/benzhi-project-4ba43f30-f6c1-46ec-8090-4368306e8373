package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"drill-seal-handover/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(sqliteConnectionLimit)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	statements := []string{
		`PRAGMA foreign_keys = ON`, `PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, task_code TEXT NOT NULL UNIQUE, site_name TEXT NOT NULL, borehole_no TEXT NOT NULL, collar_easting REAL NOT NULL, collar_northing REAL NOT NULL, total_depth_m REAL NOT NULL, strata_summary TEXT NOT NULL, status TEXT NOT NULL, version INTEGER NOT NULL, plan_version INTEGER NOT NULL DEFAULT 0, manifest_json TEXT NOT NULL DEFAULT '', manifest_digest TEXT NOT NULL DEFAULT '', frozen_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS segments (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, sequence INTEGER NOT NULL, from_depth_m REAL NOT NULL, to_depth_m REAL NOT NULL, material_type TEXT NOT NULL, planned_volume_l REAL NOT NULL, mix_ratio TEXT NOT NULL, actual_volume_l REAL NOT NULL DEFAULT 0, actual_mix_ratio TEXT NOT NULL DEFAULT '', material_batch TEXT NOT NULL DEFAULT '', performed_at TEXT, operator TEXT NOT NULL DEFAULT '', result TEXT NOT NULL, variance_percent REAL NOT NULL DEFAULT 0, version INTEGER NOT NULL, UNIQUE(task_id, sequence))`,
		`CREATE TABLE IF NOT EXISTS deviations (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, segment_id TEXT NOT NULL REFERENCES segments(id), code TEXT NOT NULL, description TEXT NOT NULL, severity TEXT NOT NULL, evidence_note TEXT NOT NULL, correction TEXT NOT NULL DEFAULT '', waiver_reason TEXT NOT NULL DEFAULT '', rework_required INTEGER NOT NULL DEFAULT 0, reviewer TEXT NOT NULL DEFAULT '', review_note TEXT NOT NULL DEFAULT '', review_result TEXT NOT NULL DEFAULT '', reviewed_at TEXT, status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(task_id, code))`,
		`CREATE TABLE IF NOT EXISTS credentials (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), serial_no TEXT NOT NULL UNIQUE, manifest_digest TEXT NOT NULL, issued_by TEXT NOT NULL, issued_at TEXT NOT NULL, payload_json TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS idempotency (task_id TEXT NOT NULL, operation TEXT NOT NULL DEFAULT '', idem_key TEXT NOT NULL, response_json TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(task_id, operation, idem_key))`,
		`CREATE TABLE IF NOT EXISTS audit_events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL, object_version INTEGER NOT NULL, idempotency_key TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL, previous_digest TEXT NOT NULL, digest TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS plan_snapshots (task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, plan_version INTEGER NOT NULL, segments_json TEXT NOT NULL, published_by TEXT NOT NULL, published_at TEXT NOT NULL, PRIMARY KEY(task_id, plan_version))`,
		`CREATE TABLE IF NOT EXISTS reworks (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, deviation_id TEXT NOT NULL REFERENCES deviations(id), segment_id TEXT NOT NULL REFERENCES segments(id), material_type TEXT NOT NULL, material_batch TEXT NOT NULL, actual_mix_ratio TEXT NOT NULL, actual_volume_l REAL NOT NULL, performed_at TEXT NOT NULL, operator TEXT NOT NULL, result TEXT NOT NULL, evidence_note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS review_history (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, deviation_id TEXT NOT NULL REFERENCES deviations(id), reviewer TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', note TEXT NOT NULL, reviewed_at TEXT NOT NULL, snapshot_json TEXT NOT NULL)`,
		`INSERT INTO schema_version(version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("数据库迁移失败: %w", err)
		}
	}
	if err := s.migrateIdempotencyOperationColumn(ctx); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	return nil
}

// migrateIdempotencyOperationColumn upgrades pre-existing idempotency tables
// that were created without the operation column. Existing cached responses
// are preserved and attributed to an empty operation so prior replays keep
// working for the operation that originally stored them.
func (s *Store) migrateIdempotencyOperationColumn(ctx context.Context) error {
	var columns []string
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(idempotency)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		columns = append(columns, name)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	hasOperation := false
	for _, name := range columns {
		if name == "operation" {
			hasOperation = true
			break
		}
	}
	if hasOperation {
		return nil
	}
	// SQLite cannot alter primary key constraints in place; rebuild the table.
	// The idempotency cache holds replayable responses only, but we still
	// perform the rebuild in a single transaction so a crash leaves either the
	// old table intact or the new table fully published.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tempName := "idempotency_new"
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS ` + tempName); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE ` + tempName + ` (task_id TEXT NOT NULL, operation TEXT NOT NULL DEFAULT '', idem_key TEXT NOT NULL, response_json TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(task_id, operation, idem_key))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ` + tempName + ` (task_id, operation, idem_key, response_json, created_at) SELECT task_id, '', idem_key, response_json, created_at FROM idempotency`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE idempotency`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE ` + tempName + ` RENAME TO idempotency`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) CreateTask(ctx context.Context, task domain.SealTask) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks(id,task_code,site_name,borehole_no,collar_easting,collar_northing,total_depth_m,strata_summary,status,version,plan_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, task.ID, task.TaskCode, task.SiteName, task.BoreholeNo, task.CollarEasting, task.CollarNorthing, task.TotalDepthM, task.StrataSummary, task.Status, task.Version, task.PlanVersion, task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (domain.SealTask, error) {
	var task domain.SealTask
	var frozen, created, updated sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,task_code,site_name,borehole_no,collar_easting,collar_northing,total_depth_m,strata_summary,status,version,plan_version,manifest_json,manifest_digest,frozen_at,created_at,updated_at FROM tasks WHERE id=?`, id).Scan(&task.ID, &task.TaskCode, &task.SiteName, &task.BoreholeNo, &task.CollarEasting, &task.CollarNorthing, &task.TotalDepthM, &task.StrataSummary, &task.Status, &task.Version, &task.PlanVersion, &task.ManifestJSON, &task.ManifestDigest, &frozen, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return task, domain.NotFound("任务", id)
	}
	if err != nil {
		return task, err
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated.String)
	if frozen.Valid && frozen.String != "" {
		value, _ := time.Parse(time.RFC3339Nano, frozen.String)
		task.FrozenAt = &value
	}
	return task, nil
}

func (s *Store) ListTasks(ctx context.Context) ([]domain.SealTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.SealTask
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		task, err := s.GetTask(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func (s *Store) FindActiveBorehole(ctx context.Context, siteName, boreholeNo string) (domain.SealTask, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks WHERE status <> ? ORDER BY created_at`, domain.TaskCredential)
	if err != nil {
		return domain.SealTask{}, false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return domain.SealTask{}, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.SealTask{}, false, err
	}
	rows.Close()
	for _, id := range ids {
		task, err := s.GetTask(ctx, id)
		if err != nil {
			return task, false, err
		}
		if domain.NormalizeSiteName(task.SiteName) == siteName && domain.NormalizeBoreholeNo(task.BoreholeNo) == boreholeNo {
			return task, true, nil
		}
	}
	return domain.SealTask{}, false, rows.Err()
}

func (s *Store) UpdateTask(ctx context.Context, task domain.SealTask, expected int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?,version=?,plan_version=?,manifest_json=?,manifest_digest=?,frozen_at=?,updated_at=? WHERE id=? AND version=?`, task.Status, task.Version, task.PlanVersion, task.ManifestJSON, task.ManifestDigest, formatTime(task.FrozenAt), task.UpdatedAt.Format(time.RFC3339Nano), task.ID, expected)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		current, e := s.GetTask(ctx, task.ID)
		if e != nil {
			return e
		}
		return domain.Conflict(current.Version)
	}
	return nil
}

func (s *Store) ReplacePlan(ctx context.Context, taskID string, segments []domain.SealSegment, planVersion int64, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldJSON string
	rows, queryErr := tx.QueryContext(ctx, `SELECT id,task_id,sequence,from_depth_m,to_depth_m,material_type,planned_volume_l,mix_ratio,actual_volume_l,actual_mix_ratio,material_batch,performed_at,operator,result,variance_percent,version FROM segments WHERE task_id=? ORDER BY sequence`, taskID)
	if queryErr != nil {
		return queryErr
	}
	var oldSegments []domain.SealSegment
	for rows.Next() {
		var seg domain.SealSegment
		var performed sql.NullString
		if err := rows.Scan(&seg.ID, &seg.TaskID, &seg.Sequence, &seg.FromDepthM, &seg.ToDepthM, &seg.MaterialType, &seg.PlannedVolumeL, &seg.MixRatio, &seg.ActualVolumeL, &seg.ActualMixRatio, &seg.MaterialBatch, &performed, &seg.Operator, &seg.Result, &seg.VariancePercent, &seg.Version); err != nil {
			rows.Close()
			return err
		}
		if performed.Valid && performed.String != "" {
			v, _ := time.Parse(time.RFC3339Nano, performed.String)
			seg.PerformedAt = &v
		}
		oldSegments = append(oldSegments, seg)
	}
	rows.Close()
	for _, seg := range oldSegments {
		if seg.Result != domain.SegmentPending || seg.PerformedAt != nil {
			return domain.ConstructionBlocks(expected, []int{seg.Sequence})
		}
	}
	if len(oldSegments) > 0 {
		encoded, _ := json.Marshal(oldSegments)
		oldJSON = string(encoded)
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status=?,version=?,plan_version=?,updated_at=? WHERE id=? AND version=?`, domain.TaskPlanned, expected+1, planVersion, time.Now().UTC().Format(time.RFC3339Nano), taskID, expected)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		var current int64
		if e := tx.QueryRowContext(ctx, `SELECT version FROM tasks WHERE id=?`, taskID).Scan(&current); e != nil {
			return e
		}
		return domain.Conflict(current)
	}
	if oldJSON != "" {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO plan_snapshots(task_id,plan_version,segments_json,published_by,published_at) VALUES(?,?,?,?,?)`, taskID, planVersion-1, oldJSON, "", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM segments WHERE task_id=?`, taskID); err != nil {
		return err
	}
	for _, seg := range segments {
		if _, err = tx.ExecContext(ctx, `INSERT INTO segments(id,task_id,sequence,from_depth_m,to_depth_m,material_type,planned_volume_l,mix_ratio,result,version) VALUES(?,?,?,?,?,?,?,?,?,?)`, seg.ID, taskID, seg.Sequence, seg.FromDepthM, seg.ToDepthM, seg.MaterialType, seg.PlannedVolumeL, seg.MixRatio, seg.Result, seg.Version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SavePlanSnapshot(ctx context.Context, snapshot domain.PlanSnapshot) error {
	encoded, err := json.Marshal(snapshot.Segments)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO plan_snapshots(task_id,plan_version,segments_json,published_by,published_at) VALUES(?,?,?,?,?)`, snapshot.TaskID, snapshot.PlanVersion, string(encoded), snapshot.PublishedBy, snapshot.PublishedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListPlanSnapshots(ctx context.Context, taskID string) ([]domain.PlanSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT task_id,plan_version,segments_json,published_by,published_at FROM plan_snapshots WHERE task_id=? ORDER BY plan_version`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PlanSnapshot
	for rows.Next() {
		var x domain.PlanSnapshot
		var raw, at string
		if err := rows.Scan(&x.TaskID, &x.PlanVersion, &raw, &x.PublishedBy, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &x.Segments)
		x.PublishedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetPlanSnapshot(ctx context.Context, taskID string, version int64) (domain.PlanSnapshot, error) {
	var x domain.PlanSnapshot
	var raw, at string
	err := s.db.QueryRowContext(ctx, `SELECT task_id,plan_version,segments_json,published_by,published_at FROM plan_snapshots WHERE task_id=? AND plan_version=?`, taskID, version).Scan(&x.TaskID, &x.PlanVersion, &raw, &x.PublishedBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return x, domain.NotFound("方案版本", fmt.Sprintf("%d", version))
	}
	if err != nil {
		return x, err
	}
	_ = json.Unmarshal([]byte(raw), &x.Segments)
	x.PublishedAt, _ = time.Parse(time.RFC3339Nano, at)
	return x, nil
}

func (s *Store) ListSegments(ctx context.Context, taskID string) ([]domain.SealSegment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,sequence,from_depth_m,to_depth_m,material_type,planned_volume_l,mix_ratio,actual_volume_l,actual_mix_ratio,material_batch,performed_at,operator,result,variance_percent,version FROM segments WHERE task_id=? ORDER BY sequence`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.SealSegment
	for rows.Next() {
		var seg domain.SealSegment
		var performed sql.NullString
		if err := rows.Scan(&seg.ID, &seg.TaskID, &seg.Sequence, &seg.FromDepthM, &seg.ToDepthM, &seg.MaterialType, &seg.PlannedVolumeL, &seg.MixRatio, &seg.ActualVolumeL, &seg.ActualMixRatio, &seg.MaterialBatch, &performed, &seg.Operator, &seg.Result, &seg.VariancePercent, &seg.Version); err != nil {
			return nil, err
		}
		if performed.Valid && performed.String != "" {
			v, _ := time.Parse(time.RFC3339Nano, performed.String)
			seg.PerformedAt = &v
		}
		result = append(result, seg)
	}
	return result, rows.Err()
}

func (s *Store) GetSegment(ctx context.Context, id string) (domain.SealSegment, error) {
	segments, err := s.ListSegments(ctx, "")
	if err != nil {
		return domain.SealSegment{}, err
	}
	for _, seg := range segments {
		if seg.ID == id {
			return seg, nil
		}
	}
	var taskID string
	err = s.db.QueryRowContext(ctx, `SELECT task_id FROM segments WHERE id=?`, id).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SealSegment{}, domain.NotFound("孔段", id)
	}
	if err != nil {
		return domain.SealSegment{}, err
	}
	segs, err := s.ListSegments(ctx, taskID)
	for _, seg := range segs {
		if seg.ID == id {
			return seg, nil
		}
	}
	return domain.SealSegment{}, err
}

func (s *Store) UpdateSegment(ctx context.Context, seg domain.SealSegment) error {
	_, err := s.db.ExecContext(ctx, `UPDATE segments SET actual_volume_l=?,actual_mix_ratio=?,material_batch=?,performed_at=?,operator=?,result=?,variance_percent=?,version=? WHERE id=?`, seg.ActualVolumeL, seg.ActualMixRatio, seg.MaterialBatch, formatTime(seg.PerformedAt), seg.Operator, seg.Result, seg.VariancePercent, seg.Version, seg.ID)
	return err
}

func (s *Store) ListMaterialUsage(ctx context.Context, taskID string) ([]domain.MaterialBatchUsage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT material_batch,material_type,actual_mix_ratio,sequence,actual_volume_l,performed_at,operator FROM segments WHERE task_id=? AND material_batch<>'' UNION ALL SELECT r.material_batch,r.material_type,r.actual_mix_ratio,s.sequence,r.actual_volume_l,r.performed_at,r.operator FROM reworks r JOIN segments s ON s.id=r.segment_id WHERE r.task_id=? ORDER BY material_batch,sequence`, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byBatch := map[string]*domain.MaterialBatchUsage{}
	var order []string
	for rows.Next() {
		var batch, mat, mix, op, at string
		var seq int
		var vol float64
		if err := rows.Scan(&batch, &mat, &mix, &seq, &vol, &at, &op); err != nil {
			return nil, err
		}
		v, _ := time.Parse(time.RFC3339Nano, at)
		x := byBatch[batch]
		if x == nil {
			x = &domain.MaterialBatchUsage{Batch: batch, MaterialType: mat, MixRatio: mix, FirstPerformedAt: v, LastPerformedAt: v}
			byBatch[batch] = x
			order = append(order, batch)
		}
		x.SegmentSequences = append(x.SegmentSequences, seq)
		x.TotalActualVolume += vol
		if v.Before(x.FirstPerformedAt) {
			x.FirstPerformedAt = v
		}
		if v.After(x.LastPerformedAt) {
			x.LastPerformedAt = v
		}
		found := false
		for _, name := range x.Operators {
			if name == op {
				found = true
			}
		}
		if !found {
			x.Operators = append(x.Operators, op)
		}
	}
	var out []domain.MaterialBatchUsage
	for _, batch := range order {
		out = append(out, *byBatch[batch])
	}
	return out, rows.Err()
}

func (s *Store) CreateRework(ctx context.Context, item domain.ReworkRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO reworks(id,task_id,deviation_id,segment_id,material_type,material_batch,actual_mix_ratio,actual_volume_l,performed_at,operator,result,evidence_note,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.TaskID, item.DeviationID, item.SegmentID, item.MaterialType, item.MaterialBatch, item.ActualMixRatio, item.ActualVolumeL, item.PerformedAt.UTC().Format(time.RFC3339Nano), item.Operator, item.Result, item.EvidenceNote, item.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListReworks(ctx context.Context, taskID string) ([]domain.ReworkRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,deviation_id,segment_id,material_type,material_batch,actual_mix_ratio,actual_volume_l,performed_at,operator,result,evidence_note,created_at FROM reworks WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReworkRecord
	for rows.Next() {
		var x domain.ReworkRecord
		var performed, created string
		if err := rows.Scan(&x.ID, &x.TaskID, &x.DeviationID, &x.SegmentID, &x.MaterialType, &x.MaterialBatch, &x.ActualMixRatio, &x.ActualVolumeL, &performed, &x.Operator, &x.Result, &x.EvidenceNote, &created); err != nil {
			return nil, err
		}
		x.PerformedAt, _ = time.Parse(time.RFC3339Nano, performed)
		x.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) CreateReview(ctx context.Context, item domain.ReviewRecord) error {
	raw, _ := json.Marshal(item.Snapshot)
	_, err := s.db.ExecContext(ctx, `INSERT INTO review_history(id,task_id,deviation_id,reviewer,result,reason,note,reviewed_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, item.TaskID, item.DeviationID, item.Reviewer, item.Result, item.Reason, item.Note, item.ReviewedAt.UTC().Format(time.RFC3339Nano), string(raw))
	return err
}
func (s *Store) ListReviews(ctx context.Context, taskID string) ([]domain.ReviewRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,deviation_id,reviewer,result,reason,note,reviewed_at,snapshot_json FROM review_history WHERE task_id=? ORDER BY reviewed_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReviewRecord
	for rows.Next() {
		var x domain.ReviewRecord
		var at, raw string
		if err := rows.Scan(&x.ID, &x.TaskID, &x.DeviationID, &x.Reviewer, &x.Result, &x.Reason, &x.Note, &at, &raw); err != nil {
			return nil, err
		}
		x.ReviewedAt, _ = time.Parse(time.RFC3339Nano, at)
		_ = json.Unmarshal([]byte(raw), &x.Snapshot)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) CreateDeviation(ctx context.Context, item domain.DeviationCase) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO deviations(id,task_id,segment_id,code,description,severity,evidence_note,correction,waiver_reason,rework_required,reviewer,review_note,review_result,reviewed_at,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.TaskID, item.SegmentID, item.Code, item.Description, item.Severity, item.EvidenceNote, item.Correction, item.WaiverReason, boolInt(item.ReworkRequired), item.Reviewer, item.ReviewNote, item.ReviewResult, formatTime(item.ReviewedAt), item.Status, item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) ListDeviations(ctx context.Context, taskID string) ([]domain.DeviationCase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,segment_id,code,description,severity,evidence_note,correction,waiver_reason,rework_required,reviewer,review_note,review_result,reviewed_at,status,created_at,updated_at FROM deviations WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DeviationCase
	for rows.Next() {
		var x domain.DeviationCase
		var rework int
		var reviewed sql.NullString
		var created, updated string
		if err := rows.Scan(&x.ID, &x.TaskID, &x.SegmentID, &x.Code, &x.Description, &x.Severity, &x.EvidenceNote, &x.Correction, &x.WaiverReason, &rework, &x.Reviewer, &x.ReviewNote, &x.ReviewResult, &reviewed, &x.Status, &created, &updated); err != nil {
			return nil, err
		}
		x.ReworkRequired = rework != 0
		x.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		x.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		if reviewed.Valid && reviewed.String != "" {
			v, _ := time.Parse(time.RFC3339Nano, reviewed.String)
			x.ReviewedAt = &v
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) GetDeviation(ctx context.Context, id string) (domain.DeviationCase, error) {
	var x domain.DeviationCase
	var rework int
	var reviewed sql.NullString
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,task_id,segment_id,code,description,severity,evidence_note,correction,waiver_reason,rework_required,reviewer,review_note,review_result,reviewed_at,status,created_at,updated_at FROM deviations WHERE id=?`, id).Scan(&x.ID, &x.TaskID, &x.SegmentID, &x.Code, &x.Description, &x.Severity, &x.EvidenceNote, &x.Correction, &x.WaiverReason, &rework, &x.Reviewer, &x.ReviewNote, &x.ReviewResult, &reviewed, &x.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return x, domain.NotFound("偏差", id)
	}
	if err != nil {
		return x, err
	}
	x.ReworkRequired = rework != 0
	x.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	x.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if reviewed.Valid && reviewed.String != "" {
		v, _ := time.Parse(time.RFC3339Nano, reviewed.String)
		x.ReviewedAt = &v
	}
	return x, nil
}
func (s *Store) UpdateDeviation(ctx context.Context, item domain.DeviationCase) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deviations SET correction=?,waiver_reason=?,rework_required=?,reviewer=?,review_note=?,review_result=?,reviewed_at=?,status=?,updated_at=? WHERE id=?`, item.Correction, item.WaiverReason, boolInt(item.ReworkRequired), item.Reviewer, item.ReviewNote, item.ReviewResult, formatTime(item.ReviewedAt), item.Status, item.UpdatedAt.Format(time.RFC3339Nano), item.ID)
	return err
}

func (s *Store) CreateCredential(ctx context.Context, c domain.HandoverCredential) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO credentials(id,task_id,serial_no,manifest_digest,issued_by,issued_at,payload_json) VALUES(?,?,?,?,?,?,?)`, c.ID, c.TaskID, c.SerialNo, c.ManifestDigest, c.IssuedBy, c.IssuedAt.Format(time.RFC3339Nano), c.PayloadJSON)
	return err
}
func (s *Store) GetCredential(ctx context.Context, taskID string) (domain.HandoverCredential, error) {
	var c domain.HandoverCredential
	var issued string
	err := s.db.QueryRowContext(ctx, `SELECT id,task_id,serial_no,manifest_digest,issued_by,issued_at,payload_json FROM credentials WHERE task_id=? ORDER BY issued_at DESC LIMIT 1`, taskID).Scan(&c.ID, &c.TaskID, &c.SerialNo, &c.ManifestDigest, &c.IssuedBy, &issued, &c.PayloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return c, domain.NotFound("凭据", taskID)
	}
	if err != nil {
		return c, err
	}
	c.IssuedAt, _ = time.Parse(time.RFC3339Nano, issued)
	return c, nil
}
func (s *Store) NextSerial(ctx context.Context) (string, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM credentials`).Scan(&n)
	return fmt.Sprintf("SH-%06d", n), err
}

func (s *Store) GetIdempotent(ctx context.Context, taskID, operation, key string) (string, bool, error) {
	if strings.TrimSpace(key) == "" {
		return "", false, nil
	}
	var response string
	err := s.db.QueryRowContext(ctx, `SELECT response_json FROM idempotency WHERE task_id=? AND operation=? AND idem_key=?`, taskID, operation, key).Scan(&response)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return response, err == nil, err
}
func (s *Store) PutIdempotent(ctx context.Context, taskID, operation, key, response string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO idempotency(task_id,operation,idem_key,response_json,created_at) VALUES(?,?,?,?,?)`, taskID, operation, key, response, time.Now().Format(time.RFC3339Nano))
	return err
}
func (s *Store) AuditEvents(ctx context.Context) ([]domain.AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,task_id,actor,action,object_version,idempotency_key,payload_json,previous_digest,digest,created_at FROM audit_events ORDER BY sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		var created string
		if err := rows.Scan(&e.Sequence, &e.TaskID, &e.Actor, &e.Action, &e.ObjectVersion, &e.IdempotencyKey, &e.PayloadJSON, &e.PreviousDigest, &e.Digest, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AuditEventsForTask(ctx context.Context, taskID string) ([]domain.AuditEvent, error) {
	events, err := s.AuditEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuditEvent, 0)
	for _, event := range events {
		if event.TaskID == taskID {
			out = append(out, event)
		}
	}
	return out, nil
}
func (s *Store) AppendAudit(ctx context.Context, e domain.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(task_id,actor,action,object_version,idempotency_key,payload_json,previous_digest,digest,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, e.TaskID, e.Actor, e.Action, e.ObjectVersion, e.IdempotencyKey, e.PayloadJSON, e.PreviousDigest, e.Digest, e.CreatedAt.Format(time.RFC3339Nano))
	return err
}
