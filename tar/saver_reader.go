package tar

import (
	"errors"
	"io"
)

type CurrType int

const (
	CurrTypeNone   CurrType = 0
	CurrTypeReg    CurrType = 1
	CurrTypeSparse CurrType = 2
)

type Checkpoint struct {
	Roffset int64

	Pad      int64
	CurrType CurrType

	RegNb int64

	SparseHoles []CheckpointSparseEntry
	SparsePos   int64
}

type CheckpointSparseEntry struct {
	Offset   int64
	NumBytes int64
}

type SaverReader interface {
	Next() (*Header, error)
	Read(buf []byte) (int, error)

	Save() (*Checkpoint, error)
}

type saverReader struct {
	tr *Reader
	cr *countingReader
}

var _ SaverReader = (*saverReader)(nil)

func NewSaverReader(r io.Reader) (SaverReader, error) {
	cr := &countingReader{
		r: r,
	}
	tr := NewReader(cr)
	return &saverReader{tr, cr}, nil
}

func (sr *saverReader) Next() (*Header, error) {
	return sr.tr.Next()
}

func (sr *saverReader) Read(buf []byte) (int, error) {
	return sr.tr.Read(buf)
}

func (sr *saverReader) Save() (*Checkpoint, error) {
	tr := sr.tr

	c := &Checkpoint{
		Roffset: sr.cr.count,
		Pad:     tr.pad,
	}

	switch curr := tr.curr.(type) {
	case nil:
	case *regFileReader:
		if curr.nb > 0 {
			c.CurrType = CurrTypeReg
			c.RegNb = curr.nb
		}
	case *sparseFileReader:
		rfr, ok := curr.fr.(*regFileReader)
		if !ok {
			return nil, errors.New("sparse file reader didn't have a regular file reader inside")
		}
		c.CurrType = CurrTypeSparse
		c.RegNb = rfr.nb
		c.SparsePos = curr.pos
		c.SparseHoles = make([]CheckpointSparseEntry, len(curr.sp))
		for i, v := range curr.sp {
			c.SparseHoles[i] = CheckpointSparseEntry{
				Offset:   v.Offset,
				NumBytes: v.Length,
			}
		}
	default:
		return nil, errors.New("unknown current tar file reader")
	}

	return c, nil
}

func (c *Checkpoint) Resume(r io.Reader) (SaverReader, error) {
	cr := &countingReader{
		r:     r,
		count: c.Roffset,
	}

	tr := &Reader{
		r:   cr,
		pad: c.Pad,
	}

	switch c.CurrType {
	case CurrTypeNone:
		tr.curr = &regFileReader{r: cr}
	case CurrTypeReg:
		tr.curr = &regFileReader{
			nb: c.RegNb,
			r:  cr,
		}
	case CurrTypeSparse:
		rfr := &regFileReader{
			nb: c.RegNb,
			r:  cr,
		}
		sfr := &sparseFileReader{
			fr:  rfr,
			pos: c.SparsePos,
			sp:  make(sparseHoles, len(c.SparseHoles)),
		}
		for i, v := range c.SparseHoles {
			sfr.sp[i] = sparseEntry{
				Offset: v.Offset,
				Length: v.NumBytes,
			}
		}
		tr.curr = sfr
	default:
		return nil, errors.New("unknown checkpoint current reader type")
	}

	return &saverReader{
		cr: cr,
		tr: tr,
	}, nil
}

type countingReader struct {
	r     io.Reader
	count int64
}

var _ io.Reader = (*countingReader)(nil)

func (cr *countingReader) Read(buf []byte) (int, error) {
	n, err := cr.r.Read(buf)
	cr.count += int64(n)
	return n, err
}
