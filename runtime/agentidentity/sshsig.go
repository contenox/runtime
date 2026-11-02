package agentidentity

import (
	"bytes"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	sshsigMagic     = "SSHSIG"
	sshsigVersion   = 1
	sshsigNamespace = "git"
	sshsigHash      = "sha512"
	sshsigBegin     = "-----BEGIN SSH SIGNATURE-----"
	sshsigEnd       = "-----END SSH SIGNATURE-----"
)

type CommitSigner struct {
	signer ssh.Signer
}

func NewCommitSigner(signer ssh.Signer) *CommitSigner {
	return &CommitSigner{signer: signer}
}

func (s *CommitSigner) Sign(message io.Reader) ([]byte, error) {
	digest := sha512.New()
	if _, err := io.Copy(digest, message); err != nil {
		return nil, err
	}
	signed := signedData(sshsigNamespace, digest.Sum(nil))
	sig, err := signWithStrongAlgorithm(s.signer, signed)
	if err != nil {
		return nil, fmt.Errorf("sign commit: %w", err)
	}
	var blob bytes.Buffer
	blob.WriteString(sshsigMagic)
	_ = binary.Write(&blob, binary.BigEndian, uint32(sshsigVersion))
	writeString(&blob, s.signer.PublicKey().Marshal())
	writeString(&blob, []byte(sshsigNamespace))
	writeString(&blob, nil)
	writeString(&blob, []byte(sshsigHash))
	writeString(&blob, ssh.Marshal(sig))
	return armor(blob.Bytes()), nil
}

func signWithStrongAlgorithm(signer ssh.Signer, data []byte) (*ssh.Signature, error) {
	if signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		algo, ok := signer.(ssh.AlgorithmSigner)
		if !ok {
			return nil, errors.New("this RSA key cannot sign with SHA-512; use an ed25519 key")
		}
		return algo.SignWithAlgorithm(rand.Reader, data, ssh.KeyAlgoRSASHA512)
	}
	return signer.Sign(rand.Reader, data)
}

func signedData(namespace string, digest []byte) []byte {
	var b bytes.Buffer
	b.WriteString(sshsigMagic)
	writeString(&b, []byte(namespace))
	writeString(&b, nil)
	writeString(&b, []byte(sshsigHash))
	writeString(&b, digest)
	return b.Bytes()
}

func writeString(b *bytes.Buffer, s []byte) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(s)))
	b.Write(s)
}

func armor(blob []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(blob)
	var out strings.Builder
	out.WriteString(sshsigBegin + "\n")
	for len(enc) > 70 {
		out.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	out.WriteString(enc + "\n")
	out.WriteString(sshsigEnd + "\n")
	return []byte(out.String())
}

func VerifySignature(armored, message []byte) (ssh.PublicKey, error) {
	text := strings.TrimSpace(string(armored))
	if !strings.HasPrefix(text, sshsigBegin) || !strings.HasSuffix(text, sshsigEnd) {
		return nil, errors.New("not an SSH signature")
	}
	body := strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimPrefix(text, sshsigBegin), sshsigEnd)), "")
	blob, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if !bytes.HasPrefix(blob, []byte(sshsigMagic)) {
		return nil, errors.New("signature has no SSHSIG preamble")
	}
	r := bytes.NewReader(blob[len(sshsigMagic):])
	var version uint32
	if err := binary.Read(r, binary.BigEndian, &version); err != nil || version != sshsigVersion {
		return nil, errors.New("unsupported SSHSIG version")
	}
	fields := make([][]byte, 5)
	for i := range fields {
		if fields[i], err = readString(r); err != nil {
			return nil, err
		}
	}
	pub, err := ssh.ParsePublicKey(fields[0])
	if err != nil {
		return nil, fmt.Errorf("signature public key: %w", err)
	}
	if string(fields[3]) != sshsigHash {
		return nil, fmt.Errorf("unsupported signature hash %q", fields[3])
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(fields[4], &sig); err != nil {
		return nil, fmt.Errorf("signature body: %w", err)
	}
	digest := sha512.Sum512(message)
	if err := pub.Verify(signedData(string(fields[1]), digest[:]), &sig); err != nil {
		return nil, fmt.Errorf("signature does not verify: %w", err)
	}
	return pub, nil
}

func readString(r *bytes.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, errors.New("truncated signature")
	}
	if int(n) > r.Len() {
		return nil, errors.New("truncated signature")
	}
	out := make([]byte, n)
	_, err := io.ReadFull(r, out)
	return out, err
}
