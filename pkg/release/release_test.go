package release

import (
	"crypto/sha256"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	priv, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, otherPub, _ := GenerateKey()
	sum := sha256.Sum256([]byte("bundle"))
	sig, err := Sign(sum[:], priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(sum[:], sig, pub); err != nil {
		t.Errorf("own key: %v", err)
	}
	if err := Verify(sum[:], sig, append(otherPub, pub...)); err != nil {
		t.Errorf("one of several keys: %v", err)
	}
	if err := Verify(sum[:], sig, otherPub); err == nil {
		t.Error("a foreign key verified the signature")
	}
	tampered := sha256.Sum256([]byte("bundle with a backdoor"))
	if err := Verify(tampered[:], sig, pub); err == nil {
		t.Error("a changed bundle verified")
	}
	if err := Verify(sum[:], sig, nil); err == nil {
		t.Error("verified with no trusted key")
	}
}
