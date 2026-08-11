package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

func TestAppleAttestorVerifiesAttestationAndRegistrationAssertion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	appID := "5DKU7FFK4X.com.fabincrm.state"
	input, root := mintAppAttestInput(t, appID, now)
	attestor, err := NewAppleAttestor(AppleAttestorConfig{
		AppID: appID,
		Roots: []*x509.Certificate{root},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAppleAttestor() error = %v", err)
	}
	if err := attestor.Verify(context.Background(), input); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	input.APNSTokenHash = "altered"
	if err := attestor.Verify(context.Background(), input); err == nil {
		t.Fatal("Verify() accepted assertion for altered registration data")
	}
}

func TestAppleAttestorRejectsDevelopmentAttestationByDefault(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	appID := "5DKU7FFK4X.com.fabincrm.state"
	input, root := mintAppAttestInputWithEnvironment(t, appID, now, []byte("appattestdevelop"))
	attestor, err := NewAppleAttestor(AppleAttestorConfig{
		AppID: appID,
		Roots: []*x509.Certificate{root},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAppleAttestor() error = %v", err)
	}
	if err := attestor.Verify(context.Background(), input); err == nil {
		t.Fatal("Verify() accepted development attestation")
	}
}

func mintAppAttestInput(t *testing.T, appID string, now time.Time) (AttestationInput, *x509.Certificate) {
	t.Helper()
	return mintAppAttestInputWithEnvironment(t, appID, now, append([]byte("appattest"), make([]byte, 7)...))
}

func mintAppAttestInputWithEnvironment(t *testing.T, appID string, now time.Time, aaguid []byte) (AttestationInput, *x509.Certificate) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate root key: %v", err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test App Attest Root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root certificate: %v", err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatalf("parse root certificate: %v", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	keyBytes := elliptic.Marshal(elliptic.P256(), leafKey.PublicKey.X, leafKey.PublicKey.Y)
	keyID := sha256.Sum256(keyBytes)
	rpID := sha256.Sum256([]byte(appID))
	authData := make([]byte, 0, 32+1+4+16+2+32+1)
	authData = append(authData, rpID[:]...)
	authData = append(authData, 0x40)
	authData = append(authData, make([]byte, 4)...)
	authData = append(authData, aaguid...)
	credentialLength := make([]byte, 2)
	binary.BigEndian.PutUint16(credentialLength, uint16(len(keyID)))
	authData = append(authData, credentialLength...)
	authData = append(authData, keyID[:]...)
	authData = append(authData, 0xa0)
	challenge := "registration-challenge"
	clientDataHash := sha256.Sum256([]byte(challenge))
	nonceInput := append(append([]byte{}, authData...), clientDataHash[:]...)
	nonce := sha256.Sum256(nonceInput)
	nonceValue, err := asn1.Marshal(struct {
		Nonce []byte `asn1:"tag:1,explicit"`
	}{Nonce: nonce[:]})
	if err != nil {
		t.Fatalf("marshal nonce extension: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Test App Attest Credential"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{
			Id:       asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2},
			Critical: false,
			Value:    nonceValue,
		}},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	attestationObject, err := cbor.Marshal(map[string]any{
		"fmt":      "apple-appattest",
		"authData": authData,
		"attStmt": map[string]any{
			"x5c":     [][]byte{leafDER},
			"receipt": []byte("receipt"),
		},
	})
	if err != nil {
		t.Fatalf("marshal attestation: %v", err)
	}
	input := AttestationInput{
		AttestationProof: AttestationProof{
			KeyID:     base64.StdEncoding.EncodeToString(keyID[:]),
			Object:    base64.StdEncoding.EncodeToString(attestationObject),
			Challenge: challenge,
		},
		APNSTokenHash: "token-hash",
		Environment:   EnvironmentSandbox,
	}
	registrationData, err := json.Marshal(struct {
		APNSTokenHash string      `json:"apns_token_hash"`
		Challenge     string      `json:"challenge"`
		Environment   Environment `json:"environment"`
	}{input.APNSTokenHash, input.Challenge, input.Environment})
	if err != nil {
		t.Fatalf("marshal registration data: %v", err)
	}
	registrationHash := sha256.Sum256(registrationData)
	assertionAuthData := make([]byte, 37)
	copy(assertionAuthData, rpID[:])
	binary.BigEndian.PutUint32(assertionAuthData[33:], 1)
	signedData := append(append([]byte{}, assertionAuthData...), registrationHash[:]...)
	signedHash := sha256.Sum256(signedData)
	signature, err := ecdsa.SignASN1(rand.Reader, leafKey, signedHash[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}
	assertion, err := cbor.Marshal(map[string]any{
		"signature":         signature,
		"authenticatorData": assertionAuthData,
	})
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	input.Assertion = base64.StdEncoding.EncodeToString(assertion)
	if input.AttestationProof.Object == input.AttestationProof.Assertion {
		t.Fatal("minted attestation and assertion unexpectedly match")
	}
	return input, root
}
