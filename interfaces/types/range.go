package types

type RangeResource struct {
	ID        string
	FileName  string
	FileSize  int64
	Available bool
}

type ByteRange struct{ Start, End int64 }
