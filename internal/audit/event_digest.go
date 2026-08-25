package audit

import "drill-seal-handover/internal/domain"

func eventDigest(canonical []byte) string {
	return domain.Digest(canonical)
}
