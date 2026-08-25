package domain

import "time"

type TaskStatus string

const (
	TaskDraft      TaskStatus = "draft"
	TaskPlanned    TaskStatus = "planned"
	TaskExecuting  TaskStatus = "executing"
	TaskReviewing  TaskStatus = "reviewing"
	TaskFrozen     TaskStatus = "frozen"
	TaskCredential TaskStatus = "credentialed"
)

type SegmentResult string

const (
	SegmentPending  SegmentResult = "pending"
	SegmentComplete SegmentResult = "complete"
	SegmentFailed   SegmentResult = "failed"
)

type DeviationStatus string

const (
	DeviationOpen      DeviationStatus = "open"
	DeviationCorrected DeviationStatus = "corrected"
	DeviationWaived    DeviationStatus = "waived"
	DeviationClosed    DeviationStatus = "closed"
	DeviationReturned  DeviationStatus = "returned"
	DeviationRework    DeviationStatus = "rework_pending"
	DeviationReady     DeviationStatus = "review_ready"
)

type ReviewResult string

const (
	ReviewPassed   ReviewResult = "passed"
	ReviewReturned ReviewResult = "returned"
)

type SealTask struct {
	ID             string     `json:"id"`
	TaskCode       string     `json:"taskCode"`
	SiteName       string     `json:"siteName"`
	BoreholeNo     string     `json:"boreholeNo"`
	CollarEasting  float64    `json:"collarEasting"`
	CollarNorthing float64    `json:"collarNorthing"`
	TotalDepthM    float64    `json:"totalDepthM"`
	StrataSummary  string     `json:"strataSummary"`
	Status         TaskStatus `json:"status"`
	Version        int64      `json:"version"`
	PlanVersion    int64      `json:"planVersion"`
	ManifestJSON   string     `json:"manifestJson,omitempty"`
	ManifestDigest string     `json:"manifestDigest,omitempty"`
	FrozenAt       *time.Time `json:"frozenAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type SealSegment struct {
	ID              string        `json:"id"`
	TaskID          string        `json:"taskId"`
	Sequence        int           `json:"sequence"`
	FromDepthM      float64       `json:"fromDepthM"`
	ToDepthM        float64       `json:"toDepthM"`
	MaterialType    string        `json:"materialType"`
	PlannedVolumeL  float64       `json:"plannedVolumeL"`
	MixRatio        string        `json:"mixRatio"`
	ActualVolumeL   float64       `json:"actualVolumeL,omitempty"`
	ActualMixRatio  string        `json:"actualMixRatio,omitempty"`
	MaterialBatch   string        `json:"materialBatch,omitempty"`
	PerformedAt     *time.Time    `json:"performedAt,omitempty"`
	Operator        string        `json:"operator,omitempty"`
	Result          SegmentResult `json:"result"`
	VariancePercent float64       `json:"variancePercent,omitempty"`
	Version         int64         `json:"version"`
}

type DeviationCase struct {
	ID             string          `json:"id"`
	TaskID         string          `json:"taskId"`
	SegmentID      string          `json:"segmentId"`
	Code           string          `json:"code"`
	Description    string          `json:"description"`
	Severity       string          `json:"severity"`
	EvidenceNote   string          `json:"evidenceNote"`
	Correction     string          `json:"correction,omitempty"`
	WaiverReason   string          `json:"waiverReason,omitempty"`
	ReworkRequired bool            `json:"reworkRequired"`
	Reviewer       string          `json:"reviewer,omitempty"`
	ReviewNote     string          `json:"reviewNote,omitempty"`
	ReviewResult   ReviewResult    `json:"reviewResult,omitempty"`
	ReviewedAt     *time.Time      `json:"reviewedAt,omitempty"`
	Status         DeviationStatus `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type HandoverCredential struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"taskId"`
	SerialNo       string    `json:"serialNo"`
	ManifestDigest string    `json:"manifestDigest"`
	IssuedBy       string    `json:"issuedBy"`
	IssuedAt       time.Time `json:"issuedAt"`
	PayloadJSON    string    `json:"payloadJson"`
}

type Aggregate struct {
	Task          SealTask             `json:"task"`
	Segments      []SealSegment        `json:"segments"`
	Deviations    []DeviationCase      `json:"deviations"`
	Credential    *HandoverCredential  `json:"credential,omitempty"`
	Progress      ProgressSummary      `json:"progress"`
	MaterialUsage []MaterialBatchUsage `json:"materialUsage"`
	Reworks       []ReworkRecord       `json:"reworks"`
	Reviews       []ReviewRecord       `json:"reviews"`
	PlanDiff      *PlanDiff            `json:"planDiff,omitempty"`
}

type PlanSnapshot struct {
	TaskID      string        `json:"taskId"`
	PlanVersion int64         `json:"planVersion"`
	Segments    []SealSegment `json:"segments"`
	PublishedBy string        `json:"publishedBy"`
	PublishedAt time.Time     `json:"publishedAt"`
}

type PlanFieldChange struct {
	Sequence int    `json:"sequence"`
	Field    string `json:"field"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
}

type PlanDiff struct {
	FromVersion int64             `json:"fromVersion"`
	ToVersion   int64             `json:"toVersion"`
	Added       []SealSegment     `json:"added"`
	Removed     []SealSegment     `json:"removed"`
	Changed     []PlanFieldChange `json:"changed"`
}

type ProgressSummary struct {
	PendingCount       int     `json:"pendingCount"`
	CompletedCount     int     `json:"completedCount"`
	FailedCount        int     `json:"failedCount"`
	TotalPlannedVolume float64 `json:"totalPlannedVolume"`
	TotalActualVolume  float64 `json:"totalActualVolume"`
	CompletionPercent  float64 `json:"completionPercent"`
}

type MaterialBatchUsage struct {
	Batch             string    `json:"batch"`
	MaterialType      string    `json:"materialType"`
	MixRatio          string    `json:"mixRatio"`
	SegmentSequences  []int     `json:"segmentSequences"`
	TotalActualVolume float64   `json:"totalActualVolume"`
	FirstPerformedAt  time.Time `json:"firstPerformedAt"`
	LastPerformedAt   time.Time `json:"lastPerformedAt"`
	Operators         []string  `json:"operators"`
}

type ReworkRecord struct {
	ID             string        `json:"id"`
	TaskID         string        `json:"taskId"`
	DeviationID    string        `json:"deviationId"`
	SegmentID      string        `json:"segmentId"`
	MaterialType   string        `json:"materialType"`
	MaterialBatch  string        `json:"materialBatch"`
	ActualMixRatio string        `json:"actualMixRatio"`
	ActualVolumeL  float64       `json:"actualVolumeL"`
	PerformedAt    time.Time     `json:"performedAt"`
	Operator       string        `json:"operator"`
	Result         SegmentResult `json:"result"`
	EvidenceNote   string        `json:"evidenceNote,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
}

type ReviewReason string

const (
	ReviewReasonMaterial ReviewReason = "material"
	ReviewReasonVolume   ReviewReason = "volume"
	ReviewReasonRatio    ReviewReason = "ratio"
	ReviewReasonEvidence ReviewReason = "evidence"
	ReviewReasonOther    ReviewReason = "other"
)

type ReviewSnapshot struct {
	PlannedVolumeL  float64 `json:"plannedVolumeL"`
	ActualVolumeL   float64 `json:"actualVolumeL"`
	DifferenceL     float64 `json:"differenceL"`
	DifferencePct   float64 `json:"differencePercent"`
	PlannedMixRatio string  `json:"plannedMixRatio"`
	ActualMixRatio  string  `json:"actualMixRatio"`
	EvidenceSummary string  `json:"evidenceSummary"`
}

type ReviewRecord struct {
	ID          string         `json:"id"`
	TaskID      string         `json:"taskId"`
	DeviationID string         `json:"deviationId"`
	Reviewer    string         `json:"reviewer"`
	Result      ReviewResult   `json:"result"`
	Reason      ReviewReason   `json:"reason,omitempty"`
	Note        string         `json:"note"`
	ReviewedAt  time.Time      `json:"reviewedAt"`
	Snapshot    ReviewSnapshot `json:"snapshot"`
}

type ReleaseBlocker struct {
	Type            string `json:"type"`
	SegmentID       string `json:"segmentId,omitempty"`
	SegmentSequence int    `json:"segmentSequence,omitempty"`
	DeviationID     string `json:"deviationId,omitempty"`
	DeviationCode   string `json:"deviationCode,omitempty"`
	Reason          string `json:"reason"`
	NextAction      string `json:"nextAction"`
}

type ReleasePreflight struct {
	Ready           bool             `json:"ready"`
	ExpectedVersion int64            `json:"expectedVersion"`
	Blockers        []ReleaseBlocker `json:"blockers"`
	ManifestJSON    string           `json:"manifestJson,omitempty"`
	ManifestDigest  string           `json:"manifestDigest,omitempty"`
}

type VerificationCheck struct {
	Name     string `json:"name"`
	Valid    bool   `json:"valid"`
	Message  string `json:"message"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
}

type CredentialVerification struct {
	Valid          bool                `json:"valid"`
	Credential     HandoverCredential  `json:"credential"`
	ManifestDigest string              `json:"manifestDigest"`
	Checks         []VerificationCheck `json:"checks"`
	AuditEvents    []AuditEvent        `json:"auditEvents"`
}

type AuditEvent struct {
	Sequence       int64     `json:"sequence"`
	TaskID         string    `json:"taskId"`
	Actor          string    `json:"actor"`
	Action         string    `json:"action"`
	ObjectVersion  int64     `json:"objectVersion"`
	IdempotencyKey string    `json:"idempotencyKey,omitempty"`
	PayloadJSON    string    `json:"payloadJson"`
	PreviousDigest string    `json:"previousDigest"`
	Digest         string    `json:"digest"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Role string

const (
	RoleWorker   Role = "worker"
	RoleManager  Role = "manager"
	RoleReviewer Role = "reviewer"
)

type Actor struct {
	Name string `json:"name"`
	Role Role   `json:"role"`
}
