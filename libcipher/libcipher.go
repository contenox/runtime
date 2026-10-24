package libcipher

import "crypto/sha256"

func CheckHash(signingKey string, salt string, shouldBe string, hash []byte) (bool, error) {
	sealed, err := NewHash(GenerateHashArgs{
		Payload:    []byte(shouldBe),
		SigningKey: []byte(signingKey),
		Salt:       []byte(salt),
	}, sha256.New)
	if err != nil {
		return false, err
	}

	return Equal(sealed, hash), nil
}
