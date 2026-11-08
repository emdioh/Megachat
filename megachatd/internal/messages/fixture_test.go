package messages

const t0 = int64(1_700_000_000_000)

const (
	sig     = "+39111"
	waLid   = "123@lid"
	waGhost = "391@c.us"
	waGrp   = "g1@g.us"
	tgGrp   = "tg-group"
)

func at(n int64) int64 { return t0 + n }
