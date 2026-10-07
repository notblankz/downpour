package engine

type PieceInfo struct {
	Index int64
	Start int64
	End   int64 // Exclusive
}

// planPieces splits totalSize into contiguous, End-exclusive pieces of at most chunkSize bytes each
func planPieces(totalSize, chunkSize int64) []PieceInfo {
	if totalSize == 0 {
		return nil
	}

	numPieces := (totalSize + chunkSize - 1) / chunkSize
	res := make([]PieceInfo, numPieces)

	for idx := range numPieces {
		start := chunkSize * idx
		pi := PieceInfo{
			Index: idx,
			Start: start,
			End:   min(start+chunkSize, totalSize),
		}

		res[idx] = pi
	}

	return res
}
