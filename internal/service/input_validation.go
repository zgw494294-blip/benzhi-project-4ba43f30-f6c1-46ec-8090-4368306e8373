package service

import "drill-seal-handover/internal/domain"

func checkExpectedVersion(expected, current int64) error {
	if expected != current {
		return domain.Conflict(current)
	}
	return nil
}
