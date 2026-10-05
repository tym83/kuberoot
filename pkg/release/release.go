// Package release signs and verifies kuberoot release bundles. The signature
// covers the bundle's SHA-256, so a bundle fetched over any transport is
// installed only if the key built into the running image vouches for it.
package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// statement is what a signature covers.
func statement(sum []byte) []byte {
	return []byte("kuberoot-bundle-v1 sha256:" + hex.EncodeToString(sum))
}

// GenerateKey returns a new key pair in PEM.
func GenerateKey() (private, public []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), nil
}

// FileSum hashes a file.
func FileSum(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// Sign returns the base64 signature of a bundle with this SHA-256.
func Sign(sum, privatePEM []byte) (string, error) {
	block, _ := pem.Decode(privatePEM)
	if block == nil {
		return "", errors.New("private key is not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return "", errors.New("private key is not ed25519")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, statement(sum))), nil
}

// Verify checks a base64 signature of a bundle with this SHA-256 against any
// of the PEM public keys in trusted.
func Verify(sum []byte, signature string, trusted []byte) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}
	found := false
	for rest := trusted; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			continue
		}
		if pub, ok := key.(ed25519.PublicKey); ok {
			found = true
			if ed25519.Verify(pub, statement(sum), sig) {
				return nil
			}
		}
	}
	if !found {
		return errors.New("no release key is trusted")
	}
	return errors.New("signature does not match any trusted release key")
}
