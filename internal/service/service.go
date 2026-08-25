package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"drill-seal-handover/internal/audit"
	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/store"
)

type Service struct {
	store *store.Store
	audit *audit.Chain
	now   func() time.Time
}

func New(repository *store.Store) *Service {
	return &Service{store: repository, audit: audit.New(repository), now: time.Now}
}

type CreateTaskInput struct {
	TaskCode       string       `json:"taskCode"`
	SiteName       string       `json:"siteName"`
	BoreholeNo     string       `json:"boreholeNo"`
	CollarEasting  float64      `json:"collarEasting"`
	CollarNorthing float64      `json:"collarNorthing"`
	TotalDepthM    float64      `json:"totalDepthM"`
	StrataSummary  string       `json:"strataSummary"`
	Actor          domain.Actor `json:"actor"`
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (domain.SealTask, error) {
	if err := requireRole(input.Actor, domain.RoleManager); err != nil {
		return domain.SealTask{}, err
	}
	now := s.now().UTC()
	task := domain.SealTask{ID: newID("task"), TaskCode: input.TaskCode, SiteName: input.SiteName, BoreholeNo: input.BoreholeNo, CollarEasting: input.CollarEasting, CollarNorthing: input.CollarNorthing, TotalDepthM: input.TotalDepthM, StrataSummary: input.StrataSummary, Status: domain.TaskDraft, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := domain.ValidateNewTask(&task); err != nil {
		return task, err
	}
	if matched, ok, err := s.store.FindActiveBorehole(ctx, task.SiteName, task.BoreholeNo); err != nil {
		return task, err
	} else if ok {
		return task, domain.DuplicateTask(matched.TaskCode)
	}
	if err := s.store.CreateTask(ctx, task); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return task, domain.Validation("taskCode", "任务编号已存在")
		}
		return task, err
	}
	if err := s.audit.Append(ctx, task.ID, input.Actor, "task.created", task.Version, "", task); err != nil {
		return task, err
	}
	return task, nil
}

type PlanSegmentInput struct {
	Sequence       int     `json:"sequence"`
	FromDepthM     float64 `json:"fromDepthM"`
	ToDepthM       float64 `json:"toDepthM"`
	MaterialType   string  `json:"materialType"`
	PlannedVolumeL float64 `json:"plannedVolumeL"`
	MixRatio       string  `json:"mixRatio"`
}
type PublishPlanInput struct {
	ExpectedVersion int64              `json:"expectedVersion"`
	Segments        []PlanSegmentInput `json:"segments"`
	Actor           domain.Actor       `json:"actor"`
}

func (s *Service) PublishPlan(ctx context.Context, taskID string, input PublishPlanInput) (domain.Aggregate, error) {
	if err := requireRole(input.Actor, domain.RoleManager); err != nil {
		return domain.Aggregate{}, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	if err := checkExpectedVersion(input.ExpectedVersion, task.Version); err != nil {
		return domain.Aggregate{}, err
	}
	segments := buildPlanSegments(taskID, input.Segments)
	if err := domain.ValidatePlan(task, segments); err != nil {
		return domain.Aggregate{}, err
	}
	current, err := s.store.ListSegments(ctx, taskID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	var blocked []int
	for _, segment := range current {
		if segment.Result != domain.SegmentPending || segment.PerformedAt != nil {
			blocked = append(blocked, segment.Sequence)
		}
	}
	if len(blocked) > 0 {
		return domain.Aggregate{}, domain.ConstructionBlocks(task.Version, blocked)
	}
	planVersion := task.PlanVersion + 1
	diff := domain.PlanDifference(task.PlanVersion, planVersion, current, segments)
	if err := s.store.ReplacePlan(ctx, taskID, segments, planVersion, task.Version); err != nil {
		return domain.Aggregate{}, err
	}
	task.Version++
	task.PlanVersion = planVersion
	task.Status = domain.TaskPlanned
	if err := s.store.SavePlanSnapshot(ctx, domain.PlanSnapshot{TaskID: taskID, PlanVersion: planVersion, Segments: segments, PublishedBy: input.Actor.Name, PublishedAt: s.now().UTC()}); err != nil {
		return domain.Aggregate{}, err
	}
	if err := s.audit.Append(ctx, taskID, input.Actor, "plan.published", task.Version, "", map[string]any{"planVersion": planVersion, "diff": diff}); err != nil {
		return domain.Aggregate{}, err
	}
	agg, err := s.GetAggregate(ctx, taskID)
	agg.PlanDiff = &diff
	return agg, err
}

func buildPlanSegments(taskID string, input []PlanSegmentInput) []domain.SealSegment {
	segments := make([]domain.SealSegment, 0, len(input))
	for _, item := range input {
		segments = append(segments, domain.SealSegment{ID: newID("seg"), TaskID: taskID, Sequence: item.Sequence, FromDepthM: item.FromDepthM, ToDepthM: item.ToDepthM, MaterialType: strings.TrimSpace(item.MaterialType), PlannedVolumeL: item.PlannedVolumeL, MixRatio: strings.TrimSpace(item.MixRatio), Result: domain.SegmentPending, Version: 1})
	}
	return segments
}

func (s *Service) PreviewPlan(ctx context.Context, taskID string, input PublishPlanInput) (domain.PlanDiff, error) {
	if err := requireRole(input.Actor, domain.RoleManager); err != nil {
		return domain.PlanDiff{}, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.PlanDiff{}, err
	}
	if task.Version != input.ExpectedVersion {
		return domain.PlanDiff{}, domain.Conflict(task.Version)
	}
	next := buildPlanSegments(taskID, input.Segments)
	if err := domain.ValidatePlan(task, next); err != nil {
		return domain.PlanDiff{}, err
	}
	current, err := s.store.ListSegments(ctx, taskID)
	if err != nil {
		return domain.PlanDiff{}, err
	}
	return domain.PlanDifference(task.PlanVersion, task.PlanVersion+1, current, next), nil
}
func (s *Service) PlanSnapshot(ctx context.Context, taskID string, version int64) (domain.PlanSnapshot, error) {
	return s.store.GetPlanSnapshot(ctx, taskID, version)
}
func (s *Service) PlanSnapshots(ctx context.Context, taskID string) ([]domain.PlanSnapshot, error) {
	return s.store.ListPlanSnapshots(ctx, taskID)
}

type ConstructionRequest struct {
	SegmentID       string               `json:"segmentId"`
	ExpectedVersion int64                `json:"expectedVersion"`
	IdempotencyKey  string               `json:"idempotencyKey"`
	ActualVolumeL   float64              `json:"actualVolumeL"`
	ActualMixRatio  string               `json:"actualMixRatio"`
	MaterialBatch   string               `json:"materialBatch"`
	PerformedAt     time.Time            `json:"performedAt"`
	Operator        string               `json:"operator"`
	Result          domain.SegmentResult `json:"result"`
	Actor           domain.Actor         `json:"actor"`
}

type ConstructionResult struct {
	Segment       domain.SealSegment    `json:"segment"`
	AutoDeviation *domain.DeviationCase `json:"autoDeviation,omitempty"`
	Replayed      bool                  `json:"replayed"`
}

func (s *Service) RecordConstruction(ctx context.Context, taskID string, input ConstructionRequest) (ConstructionResult, error) {
	if err := requireRole(input.Actor, domain.RoleWorker, domain.RoleManager); err != nil {
		return ConstructionResult{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return ConstructionResult{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return ConstructionResult{}, err
	} else if ok {
		var result ConstructionResult
		if err := json.Unmarshal([]byte(cached), &result); err != nil {
			return result, err
		}
		result.Replayed = true
		return result, nil
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return ConstructionResult{}, err
	}
	if input.ExpectedVersion != task.Version {
		return ConstructionResult{}, domain.Conflict(task.Version)
	}
	if task.Status != domain.TaskPlanned && task.Status != domain.TaskExecuting && task.Status != domain.TaskReviewing {
		return ConstructionResult{}, domain.InvalidState("任务当前状态不能登记施工")
	}
	if err := domain.ValidateConstructionTime(input.PerformedAt, task.CreatedAt, s.now().UTC()); err != nil {
		return ConstructionResult{}, err
	}
	segment, err := s.store.GetSegment(ctx, input.SegmentID)
	if err != nil {
		return ConstructionResult{}, err
	}
	if segment.TaskID != taskID {
		return ConstructionResult{}, domain.Validation("segmentId", "孔段不属于当前任务")
	}
	batch, err := domain.NormalizeBatch(input.MaterialBatch)
	if err != nil {
		return ConstructionResult{}, err
	}
	input.MaterialBatch = batch
	existingSegments, err := s.store.ListSegments(ctx, taskID)
	if err != nil {
		return ConstructionResult{}, err
	}
	for _, existing := range existingSegments {
		if existing.MaterialBatch == batch && existing.ID != segment.ID && (existing.MaterialType != segment.MaterialType || strings.TrimSpace(existing.ActualMixRatio) != strings.TrimSpace(input.ActualMixRatio)) {
			return ConstructionResult{}, domain.Validation("materialBatch", "批次 %s 与已关联孔段 %d 的材料或配比不一致", batch, existing.Sequence)
		}
	}
	existingReworks, _ := s.store.ListReworks(ctx, taskID)
	for _, existing := range existingReworks {
		if existing.MaterialBatch == batch && (existing.MaterialType != segment.MaterialType || strings.TrimSpace(existing.ActualMixRatio) != strings.TrimSpace(input.ActualMixRatio)) {
			return ConstructionResult{}, domain.Validation("materialBatch", "批次 %s 与既有返工记录的材料或配比不一致", batch)
		}
	}
	deviates, err := domain.ApplyConstruction(&segment, domain.ConstructionInput{ActualVolumeL: input.ActualVolumeL, ActualMixRatio: input.ActualMixRatio, MaterialBatch: input.MaterialBatch, PerformedAt: input.PerformedAt, Operator: input.Operator, Result: input.Result})
	if err != nil {
		return ConstructionResult{}, err
	}
	if err := s.store.UpdateSegment(ctx, segment); err != nil {
		return ConstructionResult{}, err
	}
	task.Status = domain.TaskExecuting
	task.Version++
	task.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return ConstructionResult{}, err
	}
	result := ConstructionResult{Segment: segment}
	if deviates {
		now := s.now().UTC()
		item := domain.DeviationCase{ID: newID("dev"), TaskID: taskID, SegmentID: segment.ID, Code: fmt.Sprintf("AUTO-%02d", segment.Sequence), Description: fmt.Sprintf("第 %d 段施工结果或注入量偏离方案，差异 %.2f%%", segment.Sequence, segment.VariancePercent), Severity: severity(segment.VariancePercent), EvidenceNote: "系统依据施工记录自动生成，需补充现场证据", Status: domain.DeviationOpen, CreatedAt: now, UpdatedAt: now}
		if err := s.store.CreateDeviation(ctx, item); err != nil {
			return result, err
		}
		result.AutoDeviation = &item
	}
	encoded, _ := json.Marshal(result)
	if err := s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded)); err != nil {
		return result, err
	}
	if err := s.audit.Append(ctx, taskID, input.Actor, "construction.recorded", task.Version, input.IdempotencyKey, result); err != nil {
		return result, err
	}
	return result, nil
}

func severity(variance float64) string {
	if variance < 0 {
		variance = -variance
	}
	if variance > 25 {
		return "high"
	}
	if variance > 15 {
		return "medium"
	}
	return "low"
}

type CreateDeviationInput struct {
	SegmentID       string       `json:"segmentId"`
	ExpectedVersion int64        `json:"expectedVersion"`
	IdempotencyKey  string       `json:"idempotencyKey"`
	Description     string       `json:"description"`
	Severity        string       `json:"severity"`
	EvidenceNote    string       `json:"evidenceNote"`
	Actor           domain.Actor `json:"actor"`
}

func (s *Service) CreateDeviation(ctx context.Context, taskID string, input CreateDeviationInput) (domain.DeviationCase, error) {
	if err := requireRole(input.Actor, domain.RoleWorker, domain.RoleManager); err != nil {
		return domain.DeviationCase{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return domain.DeviationCase{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return domain.DeviationCase{}, err
	} else if ok {
		var item domain.DeviationCase
		err = json.Unmarshal([]byte(cached), &item)
		return item, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.DeviationCase{}, err
	}
	if task.Status == domain.TaskFrozen || task.Status == domain.TaskCredential {
		return domain.DeviationCase{}, domain.InvalidState("移交清单已冻结，不能新增偏差")
	}
	if task.Version != input.ExpectedVersion {
		return domain.DeviationCase{}, domain.Conflict(task.Version)
	}
	seg, err := s.store.GetSegment(ctx, input.SegmentID)
	if err != nil || seg.TaskID != taskID {
		if err != nil {
			return domain.DeviationCase{}, err
		}
		return domain.DeviationCase{}, domain.Validation("segmentId", "孔段不属于当前任务")
	}
	now := s.now().UTC()
	existing, _ := s.store.ListDeviations(ctx, taskID)
	item := domain.DeviationCase{ID: newID("dev"), TaskID: taskID, SegmentID: input.SegmentID, Code: fmt.Sprintf("DEV-%03d", len(existing)+1), Description: input.Description, Severity: input.Severity, EvidenceNote: input.EvidenceNote, Status: domain.DeviationOpen, CreatedAt: now, UpdatedAt: now}
	if err := domain.ValidateDeviation(&item); err != nil {
		return item, err
	}
	if err := s.store.CreateDeviation(ctx, item); err != nil {
		return item, err
	}
	task.Version++
	task.UpdatedAt = now
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return item, err
	}
	encoded, _ := json.Marshal(item)
	_ = s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded))
	_ = s.audit.Append(ctx, taskID, input.Actor, "deviation.created", task.Version, input.IdempotencyKey, item)
	return item, nil
}

type CorrectionInput struct {
	DeviationID     string       `json:"deviationId"`
	ExpectedVersion int64        `json:"expectedVersion"`
	IdempotencyKey  string       `json:"idempotencyKey"`
	Correction      string       `json:"correction"`
	ReworkRequired  bool         `json:"reworkRequired"`
	Waive           bool         `json:"waive"`
	WaiverReason    string       `json:"waiverReason"`
	Actor           domain.Actor `json:"actor"`
}

func (s *Service) CorrectDeviation(ctx context.Context, taskID string, input CorrectionInput) (domain.DeviationCase, error) {
	if err := requireRole(input.Actor, domain.RoleManager); err != nil {
		return domain.DeviationCase{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return domain.DeviationCase{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return domain.DeviationCase{}, err
	} else if ok {
		var item domain.DeviationCase
		err = json.Unmarshal([]byte(cached), &item)
		return item, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.DeviationCase{}, err
	}
	if task.Status == domain.TaskFrozen || task.Status == domain.TaskCredential {
		return domain.DeviationCase{}, domain.InvalidState("移交清单已冻结，不能修改整改")
	}
	if task.Version != input.ExpectedVersion {
		return domain.DeviationCase{}, domain.Conflict(task.Version)
	}
	item, err := s.store.GetDeviation(ctx, input.DeviationID)
	if err != nil {
		return item, err
	}
	if item.TaskID != taskID {
		return item, domain.Validation("deviationId", "偏差不属于当前任务")
	}
	if err := domain.ApplyCorrectionDetailed(&item, input.Correction, input.WaiverReason, input.ReworkRequired, input.Waive); err != nil {
		return item, err
	}
	if input.ReworkRequired && !input.Waive {
		item.Status = domain.DeviationRework
	}
	item.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateDeviation(ctx, item); err != nil {
		return item, err
	}
	task.Version++
	task.Status = domain.TaskReviewing
	task.UpdatedAt = item.UpdatedAt
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return item, err
	}
	encoded, _ := json.Marshal(item)
	_ = s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded))
	_ = s.audit.Append(ctx, taskID, input.Actor, "deviation.corrected", task.Version, input.IdempotencyKey, item)
	return item, nil
}

type ReviewInput struct {
	DeviationID     string              `json:"deviationId"`
	ExpectedVersion int64               `json:"expectedVersion"`
	IdempotencyKey  string              `json:"idempotencyKey"`
	Note            string              `json:"note"`
	Result          domain.ReviewResult `json:"result"`
	Reason          domain.ReviewReason `json:"reason"`
	Actor           domain.Actor        `json:"actor"`
}

func (s *Service) ReviewDeviation(ctx context.Context, taskID string, input ReviewInput) (domain.DeviationCase, error) {
	if err := requireRole(input.Actor, domain.RoleReviewer); err != nil {
		return domain.DeviationCase{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return domain.DeviationCase{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return domain.DeviationCase{}, err
	} else if ok {
		var item domain.DeviationCase
		err = json.Unmarshal([]byte(cached), &item)
		return item, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.DeviationCase{}, err
	}
	if task.Status == domain.TaskFrozen || task.Status == domain.TaskCredential {
		return domain.DeviationCase{}, domain.InvalidState("移交清单已冻结，不能追加复验")
	}
	if task.Version != input.ExpectedVersion {
		return domain.DeviationCase{}, domain.Conflict(task.Version)
	}
	item, err := s.store.GetDeviation(ctx, input.DeviationID)
	if err != nil {
		return item, err
	}
	if err := domain.ApplyReview(&item, input.Actor.Name, input.Note, input.Result, s.now().UTC()); err != nil {
		return item, err
	}
	if input.Result == domain.ReviewReturned {
		switch input.Reason {
		case domain.ReviewReasonMaterial, domain.ReviewReasonVolume, domain.ReviewReasonRatio, domain.ReviewReasonEvidence, domain.ReviewReasonOther:
		default:
			return domain.DeviationCase{}, domain.Validation("reason", "退回必须选择材料、用量、配比、证据或其他原因")
		}
	}
	if input.Result == domain.ReviewPassed && item.ReworkRequired {
		reworks, _ := s.store.ListReworks(ctx, taskID)
		ready := false
		for _, rw := range reworks {
			if rw.DeviationID == item.ID && rw.Result == domain.SegmentComplete && strings.TrimSpace(rw.EvidenceNote) != "" {
				ready = true
			}
		}
		if !ready {
			return domain.DeviationCase{}, domain.InvalidState("偏差 %s 尚未完成合格返工和证据补充", item.Code)
		}
	}
	item.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateDeviation(ctx, item); err != nil {
		return item, err
	}
	segment, _ := s.store.GetSegment(ctx, item.SegmentID)
	reworks, _ := s.store.ListReworks(ctx, taskID)
	for _, rework := range reworks {
		if rework.DeviationID == item.ID && rework.Result == domain.SegmentComplete {
			segment.ActualVolumeL = rework.ActualVolumeL
			segment.ActualMixRatio = rework.ActualMixRatio
			segment.MaterialBatch = rework.MaterialBatch
			segment.Operator = rework.Operator
			segment.PerformedAt = &rework.PerformedAt
			segment.VariancePercent = domain.VolumeVariance(segment.PlannedVolumeL, rework.ActualVolumeL)
			item.EvidenceNote = strings.TrimSpace(item.EvidenceNote + " " + rework.EvidenceNote)
		}
	}
	record := domain.ReviewRecord{ID: newID("review"), TaskID: taskID, DeviationID: item.ID, Reviewer: input.Actor.Name, Result: input.Result, Reason: input.Reason, Note: item.ReviewNote, ReviewedAt: *item.ReviewedAt, Snapshot: domain.ReviewSnapshot{PlannedVolumeL: segment.PlannedVolumeL, ActualVolumeL: segment.ActualVolumeL, DifferenceL: segment.ActualVolumeL - segment.PlannedVolumeL, DifferencePct: segment.VariancePercent, PlannedMixRatio: segment.MixRatio, ActualMixRatio: segment.ActualMixRatio, EvidenceSummary: item.EvidenceNote}}
	if err := s.store.CreateReview(ctx, record); err != nil {
		return item, err
	}
	task.Version++
	task.Status = domain.TaskReviewing
	if input.Result == domain.ReviewReturned {
		task.Status = domain.TaskExecuting
	}
	task.UpdatedAt = item.UpdatedAt
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return item, err
	}
	encoded, _ := json.Marshal(item)
	_ = s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded))
	_ = s.audit.Append(ctx, taskID, input.Actor, "deviation.reviewed", task.Version, input.IdempotencyKey, item)
	return item, nil
}

type FreezeInput struct {
	ExpectedVersion  int64        `json:"expectedVersion"`
	IdempotencyKey   string       `json:"idempotencyKey"`
	PreflightVersion int64        `json:"preflightVersion,omitempty"`
	PreflightDigest  string       `json:"preflightDigest,omitempty"`
	Actor            domain.Actor `json:"actor"`
}

func (s *Service) Freeze(ctx context.Context, taskID string, input FreezeInput) (domain.SealTask, error) {
	if err := requireRole(input.Actor, domain.RoleReviewer); err != nil {
		return domain.SealTask{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return domain.SealTask{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return domain.SealTask{}, err
	} else if ok {
		var task domain.SealTask
		err = json.Unmarshal([]byte(cached), &task)
		return task, err
	}
	agg, err := s.GetAggregate(ctx, taskID)
	if err != nil {
		return domain.SealTask{}, err
	}
	if agg.Task.Version != input.ExpectedVersion {
		return agg.Task, domain.Conflict(agg.Task.Version)
	}
	if input.PreflightVersion != 0 || input.PreflightDigest != "" {
		preflight, e := s.Preflight(ctx, taskID)
		if e != nil {
			return agg.Task, e
		}
		if !preflight.Ready || preflight.ExpectedVersion != input.PreflightVersion || preflight.ManifestDigest != input.PreflightDigest {
			return agg.Task, domain.Conflict(agg.Task.Version)
		}
	}
	preflight, err := s.Preflight(ctx, taskID)
	if err != nil {
		return agg.Task, err
	}
	if !preflight.Ready {
		return agg.Task, domain.InvalidState("任务仍存在放行阻塞项")
	}
	manifest, digest := preflight.ManifestJSON, preflight.ManifestDigest
	now := s.now().UTC()
	task := agg.Task
	task.ManifestJSON = manifest
	task.ManifestDigest = digest
	task.FrozenAt = &now
	task.Status = domain.TaskFrozen
	task.Version++
	task.UpdatedAt = now
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return task, err
	}
	encoded, _ := json.Marshal(task)
	_ = s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded))
	_ = s.audit.Append(ctx, taskID, input.Actor, "manifest.frozen", task.Version, input.IdempotencyKey, map[string]string{"digest": digest})
	return task, nil
}

type IssueInput struct {
	ExpectedVersion int64        `json:"expectedVersion"`
	IdempotencyKey  string       `json:"idempotencyKey"`
	Actor           domain.Actor `json:"actor"`
}

func (s *Service) IssueCredential(ctx context.Context, taskID string, input IssueInput) (domain.HandoverCredential, error) {
	if err := requireRole(input.Actor, domain.RoleManager); err != nil {
		return domain.HandoverCredential{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return domain.HandoverCredential{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return domain.HandoverCredential{}, err
	} else if ok {
		var c domain.HandoverCredential
		err = json.Unmarshal([]byte(cached), &c)
		return c, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.HandoverCredential{}, err
	}
	if task.Version != input.ExpectedVersion {
		return domain.HandoverCredential{}, domain.Conflict(task.Version)
	}
	if task.Status != domain.TaskFrozen {
		return domain.HandoverCredential{}, domain.InvalidState("只有冻结清单才能签发凭据")
	}
	serial, err := s.store.NextSerial(ctx)
	if err != nil {
		return domain.HandoverCredential{}, err
	}
	now := s.now().UTC()
	c := domain.HandoverCredential{ID: newID("cred"), TaskID: taskID, SerialNo: serial, ManifestDigest: task.ManifestDigest, IssuedBy: input.Actor.Name, IssuedAt: now}
	c.PayloadJSON, err = audit.CredentialPayload(c.ID, c.TaskID, c.SerialNo, c.ManifestDigest, c.IssuedBy, c.IssuedAt)
	if err != nil {
		return c, err
	}
	if err := s.store.CreateCredential(ctx, c); err != nil {
		return c, err
	}
	task.Status = domain.TaskCredential
	task.Version++
	task.UpdatedAt = now
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return c, err
	}
	encoded, _ := json.Marshal(c)
	_ = s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded))
	_ = s.audit.Append(ctx, taskID, input.Actor, "credential.issued", task.Version, input.IdempotencyKey, c)
	return c, nil
}

func (s *Service) GetAggregate(ctx context.Context, taskID string) (domain.Aggregate, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	segments, err := s.store.ListSegments(ctx, taskID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	deviations, err := s.store.ListDeviations(ctx, taskID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	agg := domain.Aggregate{Task: task, Segments: segments, Deviations: deviations}
	agg.Progress = domain.Progress(segments)
	if usage, e := s.store.ListMaterialUsage(ctx, taskID); e != nil {
		return agg, e
	} else {
		agg.MaterialUsage = usage
	}
	if reworks, e := s.store.ListReworks(ctx, taskID); e != nil {
		return agg, e
	} else {
		agg.Reworks = reworks
	}
	if reviews, e := s.store.ListReviews(ctx, taskID); e != nil {
		return agg, e
	} else {
		agg.Reviews = reviews
	}
	credential, err := s.store.GetCredentialFresh(ctx, taskID)
	if err == nil {
		agg.Credential = &credential
	} else if domain.KindOf(err) != domain.KindNotFound {
		return agg, err
	}
	return agg, nil
}

func (s *Service) ConstructionProgress(ctx context.Context, taskID, result string) (domain.Aggregate, error) {
	agg, err := s.GetAggregate(ctx, taskID)
	if err != nil {
		return agg, err
	}
	if result != "" {
		filtered := make([]domain.SealSegment, 0)
		for _, seg := range agg.Segments {
			if string(seg.Result) == result {
				filtered = append(filtered, seg)
			}
		}
		agg.Segments = filtered
	}
	return agg, nil
}

func (s *Service) RecordRework(ctx context.Context, taskID string, input ReworkInput) (domain.ReworkRecord, error) {
	if err := requireRole(input.Actor, domain.RoleWorker, domain.RoleManager); err != nil {
		return domain.ReworkRecord{}, err
	}
	if err := requireKey(input.IdempotencyKey); err != nil {
		return domain.ReworkRecord{}, err
	}
	if cached, ok, err := s.store.GetIdempotent(ctx, taskID, input.IdempotencyKey); err != nil {
		return domain.ReworkRecord{}, err
	} else if ok {
		var x domain.ReworkRecord
		err = json.Unmarshal([]byte(cached), &x)
		return x, err
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.ReworkRecord{}, err
	}
	if task.Version != input.ExpectedVersion {
		return domain.ReworkRecord{}, domain.Conflict(task.Version)
	}
	item, err := s.store.GetDeviation(ctx, input.DeviationID)
	if err != nil {
		return domain.ReworkRecord{}, err
	}
	if item.TaskID != taskID {
		return domain.ReworkRecord{}, domain.Validation("deviationId", "偏差不属于当前任务")
	}
	if !item.ReworkRequired {
		return domain.ReworkRecord{}, domain.InvalidState("当前偏差未要求返工")
	}
	seg, err := s.store.GetSegment(ctx, item.SegmentID)
	if err != nil {
		return domain.ReworkRecord{}, err
	}
	if err := domain.ValidateConstructionTime(input.PerformedAt, task.CreatedAt, s.now().UTC()); err != nil {
		return domain.ReworkRecord{}, err
	}
	batch, err := domain.NormalizeBatch(input.MaterialBatch)
	if err != nil {
		return domain.ReworkRecord{}, err
	}
	segments, _ := s.store.ListSegments(ctx, taskID)
	for _, existing := range segments {
		if existing.MaterialBatch == batch && (existing.MaterialType != seg.MaterialType || strings.TrimSpace(existing.ActualMixRatio) != strings.TrimSpace(input.ActualMixRatio)) {
			return domain.ReworkRecord{}, domain.Validation("materialBatch", "批次 %s 与已关联孔段 %d 的材料或配比不一致", batch, existing.Sequence)
		}
	}
	reworks, _ := s.store.ListReworks(ctx, taskID)
	for _, existing := range reworks {
		if existing.MaterialBatch == batch && (existing.MaterialType != seg.MaterialType || strings.TrimSpace(existing.ActualMixRatio) != strings.TrimSpace(input.ActualMixRatio)) {
			return domain.ReworkRecord{}, domain.Validation("materialBatch", "批次 %s 与既有返工记录的材料或配比不一致", batch)
		}
	}
	if input.ActualVolumeL <= 0 {
		return domain.ReworkRecord{}, domain.Validation("actualVolumeL", "实际注入量必须大于 0")
	}
	if strings.TrimSpace(input.ActualMixRatio) == "" || strings.TrimSpace(input.Operator) == "" {
		return domain.ReworkRecord{}, domain.Validation("rework", "返工配比和操作者不能为空")
	}
	if input.Result != domain.SegmentComplete && input.Result != domain.SegmentFailed {
		return domain.ReworkRecord{}, domain.Validation("result", "返工结果必须为 complete 或 failed")
	}
	now := s.now().UTC()
	record := domain.ReworkRecord{ID: newID("rework"), TaskID: taskID, DeviationID: item.ID, SegmentID: seg.ID, MaterialType: seg.MaterialType, MaterialBatch: batch, ActualMixRatio: strings.TrimSpace(input.ActualMixRatio), ActualVolumeL: input.ActualVolumeL, PerformedAt: input.PerformedAt, Operator: strings.TrimSpace(input.Operator), Result: input.Result, EvidenceNote: strings.TrimSpace(input.EvidenceNote), CreatedAt: now}
	if input.Result == domain.SegmentComplete && record.EvidenceNote != "" {
		item.Status = domain.DeviationReady
	} else {
		item.Status = domain.DeviationRework
	}
	item.UpdatedAt = now
	if err := s.store.CreateRework(ctx, record); err != nil {
		return record, err
	}
	if err := s.store.UpdateDeviation(ctx, item); err != nil {
		return record, err
	}
	task.Version++
	task.Status = domain.TaskReviewing
	task.UpdatedAt = now
	if err := s.store.UpdateTask(ctx, task, input.ExpectedVersion); err != nil {
		return record, err
	}
	encoded, _ := json.Marshal(record)
	_ = s.store.PutIdempotent(ctx, taskID, input.IdempotencyKey, string(encoded))
	_ = s.audit.Append(ctx, taskID, input.Actor, "rework.recorded", task.Version, input.IdempotencyKey, record)
	return record, nil
}

type ReworkInput struct {
	DeviationID     string               `json:"deviationId"`
	ExpectedVersion int64                `json:"expectedVersion"`
	IdempotencyKey  string               `json:"idempotencyKey"`
	MaterialBatch   string               `json:"materialBatch"`
	ActualMixRatio  string               `json:"actualMixRatio"`
	ActualVolumeL   float64              `json:"actualVolumeL"`
	PerformedAt     time.Time            `json:"performedAt"`
	Operator        string               `json:"operator"`
	Result          domain.SegmentResult `json:"result"`
	EvidenceNote    string               `json:"evidenceNote"`
	Actor           domain.Actor         `json:"actor"`
}

func (s *Service) Preflight(ctx context.Context, taskID string) (domain.ReleasePreflight, error) {
	agg, err := s.GetAggregate(ctx, taskID)
	if err != nil {
		return domain.ReleasePreflight{}, err
	}
	agg = effectiveAggregate(agg)
	out := domain.ReleasePreflight{ExpectedVersion: agg.Task.Version}
	if len(agg.Segments) == 0 {
		out.Blockers = append(out.Blockers, domain.ReleaseBlocker{Type: "plan", Reason: "尚未发布封孔方案", NextAction: "编制并发布封孔方案"})
	}
	for _, seg := range agg.Segments {
		if seg.Result != domain.SegmentComplete {
			reason := "待施工"
			if seg.Result == domain.SegmentFailed {
				reason = "施工失败"
			}
			out.Blockers = append(out.Blockers, domain.ReleaseBlocker{Type: "segment", SegmentID: seg.ID, SegmentSequence: seg.Sequence, Reason: reason, NextAction: "完成或追加合格返工"})
		}
	}
	for _, item := range agg.Deviations {
		if item.Status != domain.DeviationClosed {
			out.Blockers = append(out.Blockers, domain.ReleaseBlocker{Type: "deviation", DeviationID: item.ID, DeviationCode: item.Code, Reason: string(item.Status), NextAction: "完成整改并复验通过"})
		}
	}
	if len(out.Blockers) == 0 {
		manifest, digest, e := domain.BuildManifest(agg)
		if e != nil {
			return out, e
		}
		out.Ready = true
		out.ManifestJSON = manifest
		out.ManifestDigest = digest
	}
	return out, nil
}

func effectiveAggregate(agg domain.Aggregate) domain.Aggregate {
	for index, segment := range agg.Segments {
		for _, rework := range agg.Reworks {
			if rework.SegmentID == segment.ID && rework.Result == domain.SegmentComplete {
				segment.ActualVolumeL = rework.ActualVolumeL
				segment.ActualMixRatio = rework.ActualMixRatio
				segment.MaterialBatch = rework.MaterialBatch
				segment.Operator = rework.Operator
				segment.PerformedAt = &rework.PerformedAt
				segment.Result = domain.SegmentComplete
				segment.VariancePercent = domain.VolumeVariance(segment.PlannedVolumeL, rework.ActualVolumeL)
			}
		}
		agg.Segments[index] = segment
	}
	agg.Progress = domain.Progress(agg.Segments)
	return agg
}
func (s *Service) ListTasks(ctx context.Context) ([]domain.SealTask, error) {
	return s.store.ListTasks(ctx)
}
func (s *Service) Credential(ctx context.Context, taskID string) (domain.HandoverCredential, error) {
	return s.store.GetCredential(ctx, taskID)
}
func (s *Service) VerifyCredential(ctx context.Context, taskID string) error {
	_, err := s.VerifyCredentialDetailed(ctx, taskID)
	return err
}

func (s *Service) VerifyCredentialDetailed(ctx context.Context, taskID string) (domain.CredentialVerification, error) {
	out := domain.CredentialVerification{}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return out, err
	}
	c, err := s.store.GetCredential(ctx, taskID)
	if err != nil {
		return out, err
	}
	out.Credential = c
	out.ManifestDigest = task.ManifestDigest
	checks := []domain.VerificationCheck{}
	var payload map[string]any
	if json.Unmarshal([]byte(c.PayloadJSON), &payload) != nil {
		checks = append(checks, domain.VerificationCheck{Name: "payloadJson", Message: "凭据载荷不是有效 JSON"})
	} else {
		checks = append(checks, domain.VerificationCheck{Name: "payloadJson", Valid: true, Message: "载荷 JSON 结构有效"})
		schemaOK := fmt.Sprint(payload["schemaVersion"]) == "1"
		checks = append(checks, domain.VerificationCheck{Name: "schemaVersion", Valid: schemaOK, Message: "schemaVersion 校验", Expected: "1", Actual: fmt.Sprint(payload["schemaVersion"])})
		fields := []struct{ name, expected string }{{"credentialId", c.ID}, {"taskId", c.TaskID}, {"serialNo", c.SerialNo}, {"issuedBy", c.IssuedBy}, {"issuedAt", c.IssuedAt.UTC().Format(time.RFC3339Nano)}, {"manifestDigest", c.ManifestDigest}}
		for _, field := range fields {
			actual := fmt.Sprint(payload[field.name])
			checks = append(checks, domain.VerificationCheck{Name: field.name, Valid: actual == field.expected, Message: field.name + " 校验", Expected: field.expected, Actual: actual})
		}
	}
	canonicalManifest := []byte(task.ManifestJSON)
	var manifest any
	if json.Unmarshal([]byte(task.ManifestJSON), &manifest) == nil {
		// BuildManifest already emits canonical JSON; retain its byte digest for immutable downloads.
		canonicalManifest = []byte(task.ManifestJSON)
	}
	digestOK := c.ManifestDigest == domain.Digest(canonicalManifest)
	checks = append(checks, domain.VerificationCheck{Name: "manifestDigest", Valid: digestOK, Message: "冻结清单摘要校验", Expected: task.ManifestDigest, Actual: c.ManifestDigest})
	auditErr := s.audit.Verify(ctx)
	checks = append(checks, domain.VerificationCheck{Name: "auditChain", Valid: auditErr == nil, Message: func() string {
		if auditErr != nil {
			return auditErr.Error()
		}
		return "审计链连续"
	}()})
	out.Checks = checks
	out.Valid = true
	for _, check := range checks {
		if !check.Valid {
			out.Valid = false
		}
	}
	out.AuditEvents, _ = s.store.AuditEventsForTask(ctx, taskID)
	return out, nil
}

func requireRole(actor domain.Actor, roles ...domain.Role) error {
	if !domain.ActorHasRole(actor, roles...) {
		if strings.TrimSpace(actor.Name) == "" {
			return domain.Validation("actor.name", "操作者姓名不能为空")
		}
		return domain.Forbidden("当前角色无权执行此操作")
	}
	return nil
}
func requireKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return domain.Validation("idempotencyKey", "写操作必须提供幂等键")
	}
	if len(key) > 100 {
		return domain.Validation("idempotencyKey", "幂等键不能超过 100 字符")
	}
	return nil
}
func IsDomainError(err error) bool { var target *domain.Error; return errors.As(err, &target) }
