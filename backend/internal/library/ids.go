package library

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
)

type SecureIDGenerator struct{}

func NewSecureIDGenerator() SecureIDGenerator {
	return SecureIDGenerator{}
}

func (SecureIDGenerator) NewBookID() string {
	return newUUIDv4()
}

func (SecureIDGenerator) NewChapterID() string {
	return newUUIDv4()
}

func (SecureIDGenerator) NewParagraphID() string {
	return newUUIDv4()
}

func (SecureIDGenerator) NewImportJobID() string {
	return newUUIDv4()
}

func (SecureIDGenerator) NewIdempotencyRecordID() string {
	return newUUIDv4()
}

func (SecureIDGenerator) NewDeletionTombstoneID() string {
	return newUUIDv4()
}

func (SecureIDGenerator) NewImportStorageKey() string {
	return "import/" + newOpaqueToken()
}

func newUUIDv4() string {
	var bytes [16]byte
	mustReadRandom(bytes[:])
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], bytes[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], bytes[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], bytes[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], bytes[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], bytes[10:16])
	return string(encoded[:])
}

func newOpaqueToken() string {
	var bytes [32]byte
	mustReadRandom(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func mustReadRandom(target []byte) {
	if _, err := rand.Read(target); err != nil {
		panic(err)
	}
}
