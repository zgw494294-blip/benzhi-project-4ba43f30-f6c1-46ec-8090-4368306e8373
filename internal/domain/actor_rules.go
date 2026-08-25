package domain

import "strings"

// ActorHasRole keeps role checks consistent across service commands.
func ActorHasRole(actor Actor, roles ...Role) bool {
	if strings.TrimSpace(actor.Name) == "" {
		return false
	}
	for _, role := range roles {
		if actor.Role == role {
			return true
		}
	}
	return false
}
