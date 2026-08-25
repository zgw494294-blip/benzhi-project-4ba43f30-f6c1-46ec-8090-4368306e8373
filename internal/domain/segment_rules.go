package domain

func SegmentIsComplete(segment SealSegment) bool {
	return segment.Result == SegmentComplete && segment.PerformedAt != nil
}
