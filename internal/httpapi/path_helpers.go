package httpapi

import "strings"

func taskRouteParts(requestPath string) []string {
	return strings.Split(strings.Trim(strings.TrimPrefix(requestPath, "/api/v1/tasks/"), "/"), "/")
}
