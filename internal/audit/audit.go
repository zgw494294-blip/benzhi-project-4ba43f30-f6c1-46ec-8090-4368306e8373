package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/store"
)

type Chain struct {
	store *store.Store
	now   func() time.Time
}

func New(repository *store.Store) *Chain { return &Chain{store: repository, now: time.Now} }

type eventContent struct {
	TaskID         string `json:"taskId"`
	Actor          string `json:"actor"`
	Action         string `json:"action"`
	ObjectVersion  int64  `json:"objectVersion"`
	IdempotencyKey string `json:"idempotencyKey"`
	PayloadJSON    string `json:"payloadJson"`
	PreviousDigest string `json:"previousDigest"`
	CreatedAt      string `json:"createdAt"`
}

func (c *Chain) Append(ctx context.Context, taskID string, actor domain.Actor, action string, version int64, key string, payload any) error {
	if strings.TrimSpace(actor.Name) == "" {
		return domain.Validation("actor", "审计操作者不能为空")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码审计载荷: %w", err)
	}
	events, err := c.store.AuditEvents(ctx)
	if err != nil {
		return err
	}
	previous := ""
	if len(events) > 0 {
		previous = events[len(events)-1].Digest
	}
	created := c.now().UTC()
	content := eventContent{TaskID: taskID, Actor: actor.Name, Action: action, ObjectVersion: version, IdempotencyKey: key, PayloadJSON: string(encoded), PreviousDigest: previous, CreatedAt: created.Format(time.RFC3339Nano)}
	canonical, _ := json.Marshal(content)
	event := domain.AuditEvent{TaskID: taskID, Actor: actor.Name, Action: action, ObjectVersion: version, IdempotencyKey: key, PayloadJSON: string(encoded), PreviousDigest: previous, Digest: eventDigest(canonical), CreatedAt: created}
	return c.store.AppendAudit(ctx, event)
}

func (c *Chain) Verify(ctx context.Context) error {
	events, err := c.store.AuditEvents(ctx)
	if err != nil {
		return err
	}
	previous := ""
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			return fmt.Errorf("审计序号不连续: 期望 %d，实际 %d", index+1, event.Sequence)
		}
		if event.PreviousDigest != previous {
			return fmt.Errorf("审计事件 %d 的前序摘要不匹配", event.Sequence)
		}
		content := eventContent{TaskID: event.TaskID, Actor: event.Actor, Action: event.Action, ObjectVersion: event.ObjectVersion, IdempotencyKey: event.IdempotencyKey, PayloadJSON: event.PayloadJSON, PreviousDigest: event.PreviousDigest, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano)}
		canonical, _ := json.Marshal(content)
		expected := domain.Digest(canonical)
		if expected != event.Digest {
			return fmt.Errorf("审计事件 %d 摘要无效", event.Sequence)
		}
		previous = event.Digest
	}
	return nil
}

func CredentialPayload(credentialID, taskID, serial, manifestDigest, issuedBy string, issuedAt time.Time) (string, error) {
	payload := struct {
		SchemaVersion  int    `json:"schemaVersion"`
		CredentialID   string `json:"credentialId"`
		TaskID         string `json:"taskId"`
		SerialNo       string `json:"serialNo"`
		ManifestDigest string `json:"manifestDigest"`
		IssuedBy       string `json:"issuedBy"`
		IssuedAt       string `json:"issuedAt"`
	}{1, credentialID, taskID, serial, manifestDigest, issuedBy, issuedAt.UTC().Format(time.RFC3339Nano)}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func VerifyCredential(c domain.HandoverCredential, manifestJSON string) error {
	if c.ManifestDigest != domain.Digest([]byte(manifestJSON)) {
		return domain.Validation("manifestDigest", "凭据摘要与冻结清单不匹配")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(c.PayloadJSON), &payload); err != nil {
		return domain.Validation("payloadJson", "凭据载荷不是有效 JSON")
	}
	checks := map[string]string{"credentialId": c.ID, "taskId": c.TaskID, "serialNo": c.SerialNo, "manifestDigest": c.ManifestDigest, "issuedBy": c.IssuedBy}
	for field, expected := range checks {
		if fmt.Sprint(payload[field]) != expected {
			return domain.Validation(field, "凭据字段 %s 不匹配", field)
		}
	}
	if schema := fmt.Sprint(payload["schemaVersion"]); schema != "1" {
		return domain.Validation("schemaVersion", "不支持的凭据版本 %s", strconv.Quote(schema))
	}
	return nil
}
