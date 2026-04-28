package tar

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func makeSaverReaderTestTar(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	tw := NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&Header{Name: name, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSaverReaderResumeRegularFile(t *testing.T) {
	var buf bytes.Buffer
	tw := NewWriter(&buf)
	if err := tw.WriteHeader(&Header{Name: "big.txt", Size: int64(len("hello world"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&Header{Name: "next.txt", Size: int64(len("tail"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	sr, err := NewSaverReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := sr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "big.txt" {
		t.Fatalf("first header = %q, want big.txt", hdr.Name)
	}
	var prefix [6]byte
	if _, err := io.ReadFull(sr, prefix[:]); err != nil {
		t.Fatal(err)
	}
	if string(prefix[:]) != "hello " {
		t.Fatalf("prefix = %q, want hello ", prefix)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(buf.Bytes()[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "world" {
		t.Fatalf("resumed payload = %q, want world", rest)
	}
	hdr, err = resumed.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "next.txt" {
		t.Fatalf("next header = %q, want next.txt", hdr.Name)
	}
	rest, err = io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "tail" {
		t.Fatalf("next payload = %q, want tail", rest)
	}
}

func TestSaverReaderResumeBeforeFirstNext(t *testing.T) {
	data := makeSaverReaderTestTar(t, map[string]string{
		"first.txt": "alpha",
	})

	sr, err := NewSaverReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Roffset != 0 {
		t.Fatalf("checkpoint offset = %d, want 0", checkpoint.Roffset)
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(data[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := resumed.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "first.txt" {
		t.Fatalf("resumed header = %q, want first.txt", hdr.Name)
	}
	payload, err := io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "alpha" {
		t.Fatalf("resumed payload = %q, want alpha", payload)
	}
}

func TestSaverReaderResumeAfterNextBeforeRead(t *testing.T) {
	data := makeSaverReaderTestTar(t, map[string]string{
		"first.txt": "alpha",
	})

	sr, err := NewSaverReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := sr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "first.txt" {
		t.Fatalf("header = %q, want first.txt", hdr.Name)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Roffset != blockSize {
		t.Fatalf("checkpoint offset = %d, want %d", checkpoint.Roffset, blockSize)
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(data[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "alpha" {
		t.Fatalf("resumed payload = %q, want alpha", payload)
	}
}

func TestSaverReaderResumeAfterFileEOFBeforeNext(t *testing.T) {
	data := makeSaverReaderTestTar(t, map[string]string{
		"first.txt":  "one",
		"second.txt": "two",
	})

	sr, err := NewSaverReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sr.Next(); err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(sr)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "one" {
		t.Fatalf("first payload = %q, want one", payload)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Pad == 0 {
		t.Fatal("checkpoint pad = 0, want pending padding before next header")
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(data[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := resumed.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "second.txt" {
		t.Fatalf("resumed header = %q, want second.txt", hdr.Name)
	}
	payload, err = io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "two" {
		t.Fatalf("resumed payload = %q, want two", payload)
	}
}

func TestSaverReaderResumeAfterInsecurePath(t *testing.T) {
	data := makeSaverReaderTestTar(t, map[string]string{
		"../first.txt": "alpha",
		"second.txt":   "beta",
	})

	sr, err := NewSaverReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := sr.Next()
	if !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("Next() error = %v, want ErrInsecurePath", err)
	}
	if hdr.Name != "../first.txt" {
		t.Fatalf("header = %q, want ../first.txt", hdr.Name)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(data[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "alpha" {
		t.Fatalf("resumed payload = %q, want alpha", payload)
	}
	hdr, err = resumed.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "second.txt" {
		t.Fatalf("next header = %q, want second.txt", hdr.Name)
	}
}

func TestSaverReaderResumeAtEntryBoundary(t *testing.T) {
	var buf bytes.Buffer
	tw := NewWriter(&buf)
	if err := tw.WriteHeader(&Header{Name: "first.txt", Size: int64(len("one"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&Header{Name: "second.txt", Size: int64(len("two"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	sr, err := NewSaverReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sr.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, sr); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(buf.Bytes()[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := resumed.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "second.txt" {
		t.Fatalf("resumed header = %q, want second.txt", hdr.Name)
	}
	payload, err := io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "two" {
		t.Fatalf("resumed payload = %q, want two", payload)
	}
}

func TestSaverReaderResumeSparseFile(t *testing.T) {
	f, err := os.Open("testdata/sparse-formats.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}

	sr, err := NewSaverReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := sr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hdr.Name, "sparse") {
		t.Fatalf("first header = %q, want sparse file", hdr.Name)
	}
	var prefix [64]byte
	if _, err := io.ReadFull(sr, prefix[:]); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := sr.Save()
	if err != nil {
		t.Fatal(err)
	}

	resumed, err := checkpoint.Resume(bytes.NewReader(data[checkpoint.Roffset:]))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resumed)
	if err != nil {
		t.Fatal(err)
	}

	plain := NewReader(bytes.NewReader(data))
	if _, err := plain.Next(); err != nil {
		t.Fatal(err)
	}
	wantFull, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(prefix[:], got...), wantFull) {
		t.Fatal("resumed sparse payload did not match uninterrupted read")
	}
}
