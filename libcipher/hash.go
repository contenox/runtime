package libcipher

import (
	"crypto/hmac"
	"hash"
)

type GenerateHashArgs struct {
	Payload    []byte
	SigningKey []byte
	Salt       []byte
}

type HashError string

func (e HashError) Error() string {
	return "libcipher: " + string(e)
}

func NewHash(args GenerateHashArgs, hashfn func() hash.Hash) ([]byte, error) {
	macCompute := hmac.New(hashfn, args.SigningKey)

	_, err := macCompute.Write(args.Payload)
	if err != nil {
		return nil, HashError("failed to write hash data: " + err.Error())
	}

	_, err = macCompute.Write(args.Salt)
	if err != nil {
		return nil, HashError("failed to write salt data: " + err.Error())
	}

	return macCompute.Sum(nil), nil
}

func Equal(sealedHash1, sealedHash2 []byte) bool {
	return hmac.Equal(sealedHash1, sealedHash2)
}
