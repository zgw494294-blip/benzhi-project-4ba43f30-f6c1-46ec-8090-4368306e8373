package domain

import "fmt"

type ErrorKind string

const (
	KindValidation ErrorKind = "validation"
	KindNotFound   ErrorKind = "not_found"
	KindConflict   ErrorKind = "conflict"
	KindForbidden  ErrorKind = "forbidden"
	KindState      ErrorKind = "invalid_state"
)

type Error struct {
	Kind             ErrorKind `json:"kind"`
	Field            string    `json:"field,omitempty"`
	Message          string    `json:"message"`
	Current          int64     `json:"currentVersion,omitempty"`
	MatchedTaskCode  string    `json:"matchedTaskCode,omitempty"`
	BlockingSegments []int     `json:"blockingSegments,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func Validation(field, format string, args ...any) error {
	return &Error{Kind: KindValidation, Field: field, Message: fmt.Sprintf(format, args...)}
}

func NotFound(entity, id string) error {
	return &Error{Kind: KindNotFound, Message: fmt.Sprintf("%s %s 不存在", entity, id)}
}

func Conflict(current int64) error {
	return &Error{Kind: KindConflict, Current: current, Message: fmt.Sprintf("版本冲突，当前版本为 %d", current)}
}

func DuplicateTask(taskCode string) error {
	return &Error{Kind: KindConflict, Field: "boreholeNo", MatchedTaskCode: taskCode, Message: fmt.Sprintf("同场地同钻孔存在未移交任务 %s", taskCode)}
}

func ConstructionBlocks(current int64, sequences []int) error {
	return &Error{Kind: KindState, Current: current, BlockingSegments: sequences, Message: "已有施工事实，不能修订封孔方案"}
}

func Forbidden(message string) error { return &Error{Kind: KindForbidden, Message: message} }

func InvalidState(format string, args ...any) error {
	return &Error{Kind: KindState, Message: fmt.Sprintf(format, args...)}
}

func KindOf(err error) ErrorKind {
	if value, ok := err.(*Error); ok {
		return value.Kind
	}
	return ""
}
