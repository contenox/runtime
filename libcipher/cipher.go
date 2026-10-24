package libcipher

type Encryptor interface {
	Crypt(message []byte, additionalData []byte) ([]byte, error)
}

type Decryptor interface {
	Crypt(cipherpackage []byte) ([]byte, []byte, error)
}

type (
	MessageError       string
	CipherTextError    string
	EncryptionKeyError string
	IntegrityKeyError  string
	InvalidUsageError  string
)

func (e MessageError) Error() string {
	return "libcipher: " + (string)(e)
}
func (e CipherTextError) Error() string {
	return "libcipher: " + (string)(e)
}
func (e EncryptionKeyError) Error() string {
	return "libcipher: " + (string)(e)
}
func (e IntegrityKeyError) Error() string {
	return "libcipher: " + (string)(e)
}
func (e InvalidUsageError) Error() string {
	return "libcipher: " + (string)(e)
}
