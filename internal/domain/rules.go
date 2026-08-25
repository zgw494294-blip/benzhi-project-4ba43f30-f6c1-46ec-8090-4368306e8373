package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const depthTolerance = 0.001

var batchPattern = regexp.MustCompile(`^[\p{L}\p{N}._/-]+$`)

func NormalizeSiteName(value string) string { return strings.Join(strings.Fields(value), " ") }

func NormalizeBoreholeNo(value string) string {
	return strings.ToUpper(strings.Join(strings.Fields(value), ""))
}

func ValidateNewTask(task *SealTask) error {
	task.TaskCode = strings.TrimSpace(task.TaskCode)
	task.SiteName = NormalizeSiteName(task.SiteName)
	task.BoreholeNo = NormalizeBoreholeNo(task.BoreholeNo)
	task.StrataSummary = strings.TrimSpace(task.StrataSummary)
	if task.TaskCode == "" {
		return Validation("taskCode", "任务编号不能为空")
	}
	if task.SiteName == "" {
		return Validation("siteName", "场地名称不能为空")
	}
	if task.BoreholeNo == "" {
		return Validation("boreholeNo", "钻孔编号不能为空")
	}
	if !finite(task.CollarEasting) || math.Abs(task.CollarEasting) > 100000000 || !precision(task.CollarEasting, 3) {
		return Validation("collarEasting", "孔口东坐标必须为范围内的有限数值且最多保留三位小数")
	}
	if !finite(task.CollarNorthing) || math.Abs(task.CollarNorthing) > 100000000 || !precision(task.CollarNorthing, 3) {
		return Validation("collarNorthing", "孔口北坐标必须为范围内的有限数值且最多保留三位小数")
	}
	if !finite(task.TotalDepthM) || task.TotalDepthM <= 0 || task.TotalDepthM > 20000 || !precision(task.TotalDepthM, 3) {
		return Validation("totalDepthM", "孔深必须在 0 到 20000 米之间")
	}
	if task.StrataSummary == "" {
		return Validation("strataSummary", "地层摘要不能为空")
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func precision(value float64, places int) bool {
	scale := math.Pow10(places)
	return math.Abs(value*scale-math.Round(value*scale)) < 1e-7
}

func NormalizeBatch(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" || len([]rune(value)) > 64 || !batchPattern.MatchString(value) {
		return "", Validation("materialBatch", "材料批次仅允许字母、数字、点、横线、下划线或斜线，且长度为 1 至 64 字符")
	}
	return value, nil
}

func ValidatePlan(task SealTask, segments []SealSegment) error {
	if TaskIsFrozen(task.Status) {
		return InvalidState("清单已冻结，不能修改封孔方案")
	}
	if len(segments) == 0 {
		return Validation("segments", "至少需要一个封孔段")
	}
	ordered := append([]SealSegment(nil), segments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	lastDepth := 0.0
	seen := make(map[int]bool, len(ordered))
	for index, segment := range ordered {
		if segment.Sequence != index+1 || seen[segment.Sequence] {
			return Validation("sequence", "孔段序号必须从 1 连续递增")
		}
		seen[segment.Sequence] = true
		if math.Abs(segment.FromDepthM-lastDepth) > depthTolerance {
			return Validation("fromDepthM", "第 %d 段起始深度 %.3f 与前段终点 %.3f 不连续", segment.Sequence, segment.FromDepthM, lastDepth)
		}
		if segment.ToDepthM <= segment.FromDepthM {
			return Validation("toDepthM", "第 %d 段终点必须大于起点", segment.Sequence)
		}
		if strings.TrimSpace(segment.MaterialType) == "" {
			return Validation("materialType", "第 %d 段材料类型不能为空", segment.Sequence)
		}
		if segment.PlannedVolumeL <= 0 {
			return Validation("plannedVolumeL", "第 %d 段计划注入量必须大于 0", segment.Sequence)
		}
		if strings.TrimSpace(segment.MixRatio) == "" {
			return Validation("mixRatio", "第 %d 段配比不能为空", segment.Sequence)
		}
		lastDepth = segment.ToDepthM
	}
	if math.Abs(lastDepth-task.TotalDepthM) > depthTolerance {
		return Validation("segments", "末段必须终止于孔深 %.3f 米", task.TotalDepthM)
	}
	return nil
}

type ConstructionInput struct {
	ActualVolumeL  float64       `json:"actualVolumeL"`
	ActualMixRatio string        `json:"actualMixRatio"`
	MaterialBatch  string        `json:"materialBatch"`
	PerformedAt    time.Time     `json:"performedAt"`
	Operator       string        `json:"operator"`
	Result         SegmentResult `json:"result"`
}

func ApplyConstruction(segment *SealSegment, input ConstructionInput) (bool, error) {
	if segment.Result != SegmentPending {
		return false, InvalidState("孔段 %d 已登记施工结果", segment.Sequence)
	}
	if input.ActualVolumeL <= 0 {
		return false, Validation("actualVolumeL", "实际注入量必须大于 0")
	}
	batch, err := NormalizeBatch(input.MaterialBatch)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(input.ActualMixRatio) == "" {
		return false, Validation("actualMixRatio", "实测配比不能为空")
	}
	if input.PerformedAt.IsZero() {
		return false, Validation("performedAt", "施工时间不能为空")
	}
	if strings.TrimSpace(input.Operator) == "" {
		return false, Validation("operator", "操作者不能为空")
	}
	if input.Result != SegmentComplete && input.Result != SegmentFailed {
		return false, Validation("result", "施工结果必须为 complete 或 failed")
	}
	segment.ActualVolumeL = input.ActualVolumeL
	segment.ActualMixRatio = strings.TrimSpace(input.ActualMixRatio)
	segment.MaterialBatch = batch
	segment.PerformedAt = &input.PerformedAt
	segment.Operator = strings.TrimSpace(input.Operator)
	segment.Result = input.Result
	segment.VariancePercent = VolumeVariance(segment.PlannedVolumeL, input.ActualVolumeL)
	segment.Version++
	return input.Result == SegmentFailed || math.Abs(segment.VariancePercent) > 10, nil
}

func ValidateConstructionTime(performedAt, taskCreatedAt, now time.Time) error {
	if performedAt.IsZero() {
		return Validation("performedAt", "施工时间不能为空且必须包含时区")
	}
	if performedAt.Before(taskCreatedAt) {
		return Validation("performedAt", "施工时间不得早于任务创建时间")
	}
	if performedAt.After(now.Add(5 * time.Minute)) {
		return Validation("performedAt", "施工时间超过服务器允许的未来偏差")
	}
	return nil
}

func PlanDifference(fromVersion, toVersion int64, oldSegments, newSegments []SealSegment) PlanDiff {
	diff := PlanDiff{FromVersion: fromVersion, ToVersion: toVersion}
	oldBySeq := make(map[int]SealSegment, len(oldSegments))
	newBySeq := make(map[int]SealSegment, len(newSegments))
	for _, value := range oldSegments {
		oldBySeq[value.Sequence] = value
	}
	for _, value := range newSegments {
		newBySeq[value.Sequence] = value
	}
	for sequence, before := range oldBySeq {
		after, ok := newBySeq[sequence]
		if !ok {
			diff.Removed = append(diff.Removed, before)
			continue
		}
		fields := []struct {
			name          string
			before, after any
			changed       bool
		}{
			{"fromDepthM", before.FromDepthM, after.FromDepthM, before.FromDepthM != after.FromDepthM},
			{"toDepthM", before.ToDepthM, after.ToDepthM, before.ToDepthM != after.ToDepthM},
			{"materialType", before.MaterialType, after.MaterialType, before.MaterialType != after.MaterialType},
			{"plannedVolumeL", before.PlannedVolumeL, after.PlannedVolumeL, before.PlannedVolumeL != after.PlannedVolumeL},
			{"mixRatio", before.MixRatio, after.MixRatio, before.MixRatio != after.MixRatio},
		}
		for _, field := range fields {
			if field.changed {
				diff.Changed = append(diff.Changed, PlanFieldChange{Sequence: sequence, Field: field.name, Before: field.before, After: field.after})
			}
		}
	}
	for sequence, after := range newBySeq {
		if _, ok := oldBySeq[sequence]; !ok {
			diff.Added = append(diff.Added, after)
		}
	}
	sort.Slice(diff.Added, func(i, j int) bool { return diff.Added[i].Sequence < diff.Added[j].Sequence })
	sort.Slice(diff.Removed, func(i, j int) bool { return diff.Removed[i].Sequence < diff.Removed[j].Sequence })
	sort.Slice(diff.Changed, func(i, j int) bool {
		if diff.Changed[i].Sequence == diff.Changed[j].Sequence {
			return diff.Changed[i].Field < diff.Changed[j].Field
		}
		return diff.Changed[i].Sequence < diff.Changed[j].Sequence
	})
	return diff
}

func Progress(segments []SealSegment) ProgressSummary {
	var out ProgressSummary
	for _, segment := range segments {
		out.TotalPlannedVolume += segment.PlannedVolumeL
		out.TotalActualVolume += segment.ActualVolumeL
		switch segment.Result {
		case SegmentPending:
			out.PendingCount++
		case SegmentComplete:
			out.CompletedCount++
		case SegmentFailed:
			out.FailedCount++
		}
	}
	if len(segments) > 0 {
		out.CompletionPercent = math.Round(float64(out.CompletedCount)*10000/float64(len(segments))) / 100
	}
	return out
}

func VolumeVariance(planned, actual float64) float64 {
	if planned == 0 {
		return 0
	}
	return math.Round(((actual-planned)/planned)*10000) / 100
}

func ValidateDeviation(item *DeviationCase) error {
	item.Description = strings.TrimSpace(item.Description)
	item.EvidenceNote = strings.TrimSpace(item.EvidenceNote)
	item.Severity = strings.TrimSpace(item.Severity)
	if item.SegmentID == "" {
		return Validation("segmentId", "偏差必须关联孔段")
	}
	if item.Description == "" {
		return Validation("description", "偏差描述不能为空")
	}
	if item.EvidenceNote == "" {
		return Validation("evidenceNote", "偏差证据说明不能为空")
	}
	switch item.Severity {
	case "low", "medium", "high":
	default:
		return Validation("severity", "严重程度必须为 low、medium 或 high")
	}
	return nil
}

func ApplyCorrection(item *DeviationCase, correction string, reworkRequired, waive bool) error {
	return ApplyCorrectionDetailed(item, correction, "", reworkRequired, waive)
}

func ApplyCorrectionDetailed(item *DeviationCase, correction, waiverReason string, reworkRequired, waive bool) error {
	if item.Status == DeviationClosed {
		return InvalidState("已闭合偏差不能再次整改")
	}
	correction = strings.TrimSpace(correction)
	if correction == "" {
		return Validation("correction", "整改措施不能为空")
	}
	item.Correction = correction
	item.ReworkRequired = reworkRequired
	item.Reviewer = ""
	item.ReviewNote = ""
	item.ReviewResult = ""
	item.ReviewedAt = nil
	if waive {
		if item.Severity == "high" {
			return Forbidden("高严重程度偏差不能直接豁免")
		}
		if strings.TrimSpace(waiverReason) == "" {
			return Validation("waiverReason", "无需整改必须填写豁免理由")
		}
		item.WaiverReason = strings.TrimSpace(waiverReason)
		item.Status = DeviationWaived
	} else {
		item.Status = DeviationCorrected
	}
	return nil
}

func ApplyReview(item *DeviationCase, reviewer, note string, result ReviewResult, reviewedAt time.Time) error {
	if item.Status != DeviationCorrected && item.Status != DeviationWaived && item.Status != DeviationReturned && item.Status != DeviationReady {
		return InvalidState("偏差 %s 尚未提交整改", item.Code)
	}
	if strings.TrimSpace(reviewer) == "" || strings.TrimSpace(note) == "" {
		return Validation("review", "复核员和复验意见不能为空")
	}
	if result != ReviewPassed && result != ReviewReturned {
		return Validation("reviewResult", "复验结论必须为 passed 或 returned")
	}
	item.Reviewer = strings.TrimSpace(reviewer)
	item.ReviewNote = strings.TrimSpace(note)
	item.ReviewResult = result
	item.ReviewedAt = &reviewedAt
	if result == ReviewPassed {
		item.Status = DeviationClosed
	} else {
		item.Status = DeviationReturned
	}
	return nil
}

func CheckRelease(aggregate Aggregate) error {
	if len(aggregate.Segments) == 0 {
		return InvalidState("尚未发布封孔方案")
	}
	for _, segment := range aggregate.Segments {
		if segment.Result != SegmentComplete {
			return InvalidState("孔段 %d 尚未完成或施工失败", segment.Sequence)
		}
	}
	for _, item := range aggregate.Deviations {
		if item.Status != DeviationClosed {
			return InvalidState("偏差 %s 尚未通过复验", item.Code)
		}
	}
	return nil
}

type Manifest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Task          ManifestTask        `json:"task"`
	Segments      []ManifestSegment   `json:"segments"`
	Deviations    []ManifestDeviation `json:"deviations"`
}

type ManifestTask struct {
	ID            string  `json:"id"`
	TaskCode      string  `json:"taskCode"`
	SiteName      string  `json:"siteName"`
	BoreholeNo    string  `json:"boreholeNo"`
	TotalDepthM   float64 `json:"totalDepthM"`
	StrataSummary string  `json:"strataSummary"`
	PlanVersion   int64   `json:"planVersion"`
}

type ManifestSegment struct {
	Sequence        int     `json:"sequence"`
	FromDepthM      float64 `json:"fromDepthM"`
	ToDepthM        float64 `json:"toDepthM"`
	MaterialType    string  `json:"materialType"`
	PlannedVolumeL  float64 `json:"plannedVolumeL"`
	ActualVolumeL   float64 `json:"actualVolumeL"`
	MixRatio        string  `json:"mixRatio"`
	ActualMixRatio  string  `json:"actualMixRatio"`
	MaterialBatch   string  `json:"materialBatch"`
	Operator        string  `json:"operator"`
	VariancePercent float64 `json:"variancePercent"`
}

type ManifestDeviation struct {
	Code         string `json:"code"`
	SegmentID    string `json:"segmentId"`
	Description  string `json:"description"`
	Correction   string `json:"correction"`
	Reviewer     string `json:"reviewer"`
	ReviewResult string `json:"reviewResult"`
}

func BuildManifest(aggregate Aggregate) (string, string, error) {
	if err := CheckRelease(aggregate); err != nil {
		return "", "", err
	}
	manifest := Manifest{SchemaVersion: CurrentManifestSchemaVersion}
	manifest.Task = ManifestTask{ID: aggregate.Task.ID, TaskCode: aggregate.Task.TaskCode, SiteName: aggregate.Task.SiteName, BoreholeNo: aggregate.Task.BoreholeNo, TotalDepthM: aggregate.Task.TotalDepthM, StrataSummary: aggregate.Task.StrataSummary, PlanVersion: aggregate.Task.PlanVersion}
	segments := append([]SealSegment(nil), aggregate.Segments...)
	sort.Slice(segments, func(i, j int) bool { return segments[i].Sequence < segments[j].Sequence })
	for _, segment := range segments {
		manifest.Segments = append(manifest.Segments, ManifestSegment{Sequence: segment.Sequence, FromDepthM: segment.FromDepthM, ToDepthM: segment.ToDepthM, MaterialType: segment.MaterialType, PlannedVolumeL: segment.PlannedVolumeL, ActualVolumeL: segment.ActualVolumeL, MixRatio: segment.MixRatio, ActualMixRatio: segment.ActualMixRatio, MaterialBatch: segment.MaterialBatch, Operator: segment.Operator, VariancePercent: segment.VariancePercent})
	}
	deviations := append([]DeviationCase(nil), aggregate.Deviations...)
	sort.Slice(deviations, func(i, j int) bool { return deviations[i].Code < deviations[j].Code })
	for _, item := range deviations {
		manifest.Deviations = append(manifest.Deviations, ManifestDeviation{Code: item.Code, SegmentID: item.SegmentID, Description: item.Description, Correction: item.Correction, Reviewer: item.Reviewer, ReviewResult: string(item.ReviewResult)})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", "", fmt.Errorf("编码移交清单: %w", err)
	}
	return string(encoded), Digest(encoded), nil
}

func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func CanTransition(from, to TaskStatus) bool {
	allowed := map[TaskStatus][]TaskStatus{
		TaskDraft:     {TaskPlanned},
		TaskPlanned:   {TaskPlanned, TaskExecuting},
		TaskExecuting: {TaskExecuting, TaskReviewing, TaskFrozen},
		TaskReviewing: {TaskReviewing, TaskExecuting, TaskFrozen},
		TaskFrozen:    {TaskCredential},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
