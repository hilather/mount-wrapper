package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Content fingerprint samples (parity with Python scanner).
const (
	hashHead = 1 * 1024 * 1024 // first 1 MiB
	hashTail = 64 * 1024       // last 64 KiB
)

// ComputeFingerprint builds archive fingerprint: size:mtime_ns or size:mtime_ns:hash16.
func ComputeFingerprint(path string, sizeBytes, mtimeNs int64, content bool) (string, error) {
	base := fmt.Sprintf("%d:%d", sizeBytes, mtimeNs)
	if !content {
		return base, nil
	}
	digest, err := PartialContentHash(path, sizeBytes)
	if err != nil {
		return "", err
	}
	return base + ":" + digest, nil
}

// PartialContentHash is SHA-256 of first 1 MiB + last 64 KiB; returns first 16 hex chars.
func PartialContentHash(path string, sizeBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	size := sizeBytes
	if size < 0 {
		st, err := f.Stat()
		if err != nil {
			return "", err
		}
		size = st.Size()
	}

	h := sha256.New()
	head := make([]byte, hashHead)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	h.Write(head[:n])

	if size > hashHead {
		tailStart := size - hashTail
		if tailStart < hashHead {
			tailStart = hashHead
		}
		if tailStart < size {
			if _, err := f.Seek(tailStart, io.SeekStart); err != nil {
				return "", err
			}
			tail := make([]byte, hashTail)
			tn, err := io.ReadFull(f, tail)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return "", err
			}
			h.Write(tail[:tn])
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:16], nil
}
