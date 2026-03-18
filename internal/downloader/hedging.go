package downloader

type HedgeTicket struct {
	Task                 *ChunkTask
	ChunkVersionSnapshot int32
}

type HedgePhase int

const (
	HedgePhaseEarly HedgePhase = 0
	HedgePhaseLate  HedgePhase = 1
)
