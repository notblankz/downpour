// internal/downloader/checksum.go
package downloader

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
)

type ChecksumInfo struct {
	ExpectedHash string
	AlgoName     string
	Algo         ChecksumAlgo
}

type ChecksumAlgo struct {
	ChecksumLen int
	NewHash     func() hash.Hash
}

var SupportedChecksum = map[string]ChecksumAlgo{
	"md5":    {ChecksumLen: 32, NewHash: md5.New},
	"sha1":   {ChecksumLen: 40, NewHash: sha1.New},
	"sha256": {ChecksumLen: 64, NewHash: sha256.New},
	"sha384": {ChecksumLen: 96, NewHash: sha512.New384},
	"sha512": {ChecksumLen: 128, NewHash: sha512.New},
}

func VerifyFile(filepath string, expectedHash string, algoInfo ChecksumAlgo) error {

	f, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open the file %q - %w", filepath, err)
	}
	defer f.Close()

	hash := algoInfo.NewHash()

	if _, err := io.Copy(hash, f); err != nil {
		return fmt.Errorf("failed to hash file stream - %w", err)
	}

	calculatedHashBytes := hash.Sum(nil)

	calculatedHash := hex.EncodeToString(calculatedHashBytes)

	if calculatedHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s | got %s", expectedHash, calculatedHash)
	}
	return nil
}
