package domain

func TaskIsFrozen(status TaskStatus) bool {
	return status == TaskFrozen || status == TaskCredential
}
