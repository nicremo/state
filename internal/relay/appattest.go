package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
)

var appAttestNonceOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}

var (
	appAttestProductionAAGUID  = append([]byte("appattest"), make([]byte, 7)...)
	appAttestDevelopmentAAGUID = []byte("appattestdevelop")
)

const appleAppAttestationRootPEM = `-----BEGIN CERTIFICATE-----
MIICITCCAaegAwIBAgIQC/O+DvHN0uD7jG5yH2IXmDAKBggqhkjOPQQDAzBSMSYw
JAYDVQQDDB1BcHBsZSBBcHAgQXR0ZXN0YXRpb24gUm9vdCBDQTETMBEGA1UECgwK
QXBwbGUgSW5jLjETMBEGA1UECAwKQ2FsaWZvcm5pYTAeFw0yMDAzMTgxODMyNTNa
Fw00NTAzMTUwMDAwMDBaMFIxJjAkBgNVBAMMHUFwcGxlIEFwcCBBdHRlc3RhdGlv
biBSb290IENBMRMwEQYDVQQKDApBcHBsZSBJbmMuMRMwEQYDVQQIDApDYWxpZm9y
bmlhMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAERTHhmLW07ATaFQIEVwTtT4dyctdh
NbJhFs/Ii2FdCgAHGbpphY3+d8qjuDngIN3WVhQUBHAoMeQ/cLiP1sOUtgjqK9au
Yen1mMEvRq9Sk3Jm5X8U62H+xTD3FE9TgS41o0IwQDAPBgNVHRMBAf8EBTADAQH/
MB0GA1UdDgQWBBSskRBTM72+aEH/pwyp5frq5eWKoTAOBgNVHQ8BAf8EBAMCAQYw
CgYIKoZIzj0EAwMDaAAwZQIwQgFGnByvsiVbpTKwSga0kP0e8EeDS4+sQmTvb7vn
53O5+FRXgeLhpJ06ysC5PrOyAjEAp5U4xDgEgllF7En3VcE3iexZZtKeYnpqtijV
oyFraWVIyd/dganmrduC1bmTBGwD
-----END CERTIFICATE-----`

type AppleAttestorConfig struct {
	AppID            string
	Roots            []*x509.Certificate
	Clock            func() time.Time
	AllowDevelopment bool
}

type AppleAttestor struct {
	appID            string
	roots            *x509.CertPool
	clock            func() time.Time
	allowDevelopment bool
}

type appleAttestationObject struct {
	Format    string                    `cbor:"fmt"`
	AuthData  []byte                    `cbor:"authData"`
	Statement appleAttestationStatement `cbor:"attStmt"`
}

type appleAttestationStatement struct {
	Certificates [][]byte `cbor:"x5c"`
	Receipt      []byte   `cbor:"receipt"`
}

type appleAssertion struct {
	Signature         []byte `cbor:"signature"`
	AuthenticatorData []byte `cbor:"authenticatorData"`
}

type parsedAttestationAuthData struct {
	RelyingPartyHash []byte
	Flags            byte
	SignCount        uint32
	AAGUID           []byte
	CredentialID     []byte
}

func NewAppleAttestor(config AppleAttestorConfig) (*AppleAttestor, error) {
	if config.AppID == "" {
		return nil, errors.New("App Attest application ID is required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	roots := x509.NewCertPool()
	if len(config.Roots) == 0 {
		block, _ := pem.Decode([]byte(appleAppAttestationRootPEM))
		if block == nil {
			return nil, errors.New("decode Apple App Attestation root certificate")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse Apple App Attestation root certificate: %w", err)
		}
		roots.AddCert(certificate)
	} else {
		for _, certificate := range config.Roots {
			if certificate == nil {
				return nil, errors.New("App Attest root certificate is nil")
			}
			roots.AddCert(certificate)
		}
	}
	return &AppleAttestor{
		appID:            config.AppID,
		roots:            roots,
		clock:            config.Clock,
		allowDevelopment: config.AllowDevelopment,
	}, nil
}

func (attestor *AppleAttestor) Verify(ctx context.Context, input AttestationInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	attestationBytes, err := decodeAppAttestValue(input.Object)
	if err != nil {
		return ErrInvalidAttestation
	}
	assertionBytes, err := decodeAppAttestValue(input.Assertion)
	if err != nil {
		return ErrInvalidAttestation
	}
	keyID, err := decodeAppAttestValue(input.KeyID)
	if err != nil || len(keyID) != sha256.Size {
		return ErrInvalidAttestation
	}
	var object appleAttestationObject
	if err := cbor.Unmarshal(attestationBytes, &object); err != nil {
		return ErrInvalidAttestation
	}
	if object.Format != "apple-appattest" || len(object.Statement.Certificates) == 0 || len(object.AuthData) < 55 {
		return ErrInvalidAttestation
	}
	leaf, err := attestor.verifyCertificateChain(object.Statement.Certificates)
	if err != nil {
		return ErrInvalidAttestation
	}
	publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return ErrInvalidAttestation
	}
	authData, err := parseAttestationAuthData(object.AuthData)
	if err != nil {
		return ErrInvalidAttestation
	}
	if err := attestor.verifyAuthenticatorData(authData, keyID, publicKey); err != nil {
		return ErrInvalidAttestation
	}
	challengeHash := sha256.Sum256([]byte(input.Challenge))
	nonceInput := make([]byte, 0, len(object.AuthData)+sha256.Size)
	nonceInput = append(nonceInput, object.AuthData...)
	nonceInput = append(nonceInput, challengeHash[:]...)
	expectedNonce := sha256.Sum256(nonceInput)
	certificateNonce, err := appAttestCertificateNonce(leaf)
	if err != nil || subtle.ConstantTimeCompare(certificateNonce, expectedNonce[:]) != 1 {
		return ErrInvalidAttestation
	}
	var assertion appleAssertion
	if err := cbor.Unmarshal(assertionBytes, &assertion); err != nil {
		return ErrInvalidAttestation
	}
	if err := attestor.verifyAssertion(publicKey, assertion, input); err != nil {
		return ErrInvalidAttestation
	}
	return nil
}

func (attestor *AppleAttestor) verifyCertificateChain(encodedCertificates [][]byte) (*x509.Certificate, error) {
	certificates := make([]*x509.Certificate, 0, len(encodedCertificates))
	for _, encoded := range encodedCertificates {
		certificate, err := x509.ParseCertificate(encoded)
		if err != nil {
			return nil, err
		}
		certificates = append(certificates, certificate)
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range certificates[1:] {
		intermediates.AddCert(certificate)
	}
	_, err := certificates[0].Verify(x509.VerifyOptions{
		Roots:         attestor.roots,
		Intermediates: intermediates,
		CurrentTime:   attestor.clock(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return nil, err
	}
	return certificates[0], nil
}

func (attestor *AppleAttestor) verifyAuthenticatorData(authData parsedAttestationAuthData, keyID []byte, publicKey *ecdsa.PublicKey) error {
	expectedRelyingParty := sha256.Sum256([]byte(attestor.appID))
	if subtle.ConstantTimeCompare(authData.RelyingPartyHash, expectedRelyingParty[:]) != 1 || authData.Flags&0x40 == 0 || authData.SignCount != 0 {
		return ErrInvalidAttestation
	}
	validEnvironment := subtle.ConstantTimeCompare(authData.AAGUID, appAttestProductionAAGUID) == 1
	if attestor.allowDevelopment {
		validEnvironment = validEnvironment || subtle.ConstantTimeCompare(authData.AAGUID, appAttestDevelopmentAAGUID) == 1
	}
	if !validEnvironment {
		return ErrInvalidAttestation
	}
	publicKeyBytes := elliptic.Marshal(elliptic.P256(), publicKey.X, publicKey.Y)
	publicKeyHash := sha256.Sum256(publicKeyBytes)
	if subtle.ConstantTimeCompare(publicKeyHash[:], authData.CredentialID) != 1 || subtle.ConstantTimeCompare(publicKeyHash[:], keyID) != 1 {
		return ErrInvalidAttestation
	}
	return nil
}

func (attestor *AppleAttestor) verifyAssertion(publicKey *ecdsa.PublicKey, assertion appleAssertion, input AttestationInput) error {
	if len(assertion.AuthenticatorData) < 37 || len(assertion.Signature) == 0 {
		return ErrInvalidAttestation
	}
	expectedRelyingParty := sha256.Sum256([]byte(attestor.appID))
	if subtle.ConstantTimeCompare(assertion.AuthenticatorData[:32], expectedRelyingParty[:]) != 1 {
		return ErrInvalidAttestation
	}
	if binary.BigEndian.Uint32(assertion.AuthenticatorData[33:37]) == 0 {
		return ErrInvalidAttestation
	}
	clientDataHash, err := registrationClientDataHash(input)
	if err != nil {
		return err
	}
	signedData := make([]byte, 0, len(assertion.AuthenticatorData)+sha256.Size)
	signedData = append(signedData, assertion.AuthenticatorData...)
	signedData = append(signedData, clientDataHash[:]...)
	digest := sha256.Sum256(signedData)
	if !ecdsa.VerifyASN1(publicKey, digest[:], assertion.Signature) {
		return ErrInvalidAttestation
	}
	return nil
}

func parseAttestationAuthData(value []byte) (parsedAttestationAuthData, error) {
	if len(value) < 55 {
		return parsedAttestationAuthData{}, ErrInvalidAttestation
	}
	credentialLength := int(binary.BigEndian.Uint16(value[53:55]))
	if credentialLength < 1 || len(value) < 55+credentialLength {
		return parsedAttestationAuthData{}, ErrInvalidAttestation
	}
	return parsedAttestationAuthData{
		RelyingPartyHash: append([]byte(nil), value[:32]...),
		Flags:            value[32],
		SignCount:        binary.BigEndian.Uint32(value[33:37]),
		AAGUID:           append([]byte(nil), value[37:53]...),
		CredentialID:     append([]byte(nil), value[55:55+credentialLength]...),
	}, nil
}

func appAttestCertificateNonce(certificate *x509.Certificate) ([]byte, error) {
	for _, extension := range certificate.Extensions {
		if extension.Id.Equal(appAttestNonceOID) {
			container := struct {
				Nonce []byte `asn1:"tag:1,explicit"`
			}{}
			rest, err := asn1.Unmarshal(extension.Value, &container)
			if err != nil || len(rest) != 0 || len(container.Nonce) != sha256.Size {
				return nil, ErrInvalidAttestation
			}
			return container.Nonce, nil
		}
	}
	return nil, ErrInvalidAttestation
}

func registrationClientDataHash(input AttestationInput) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(struct {
		APNSTokenHash string      `json:"apns_token_hash"`
		Challenge     string      `json:"challenge"`
		Environment   Environment `json:"environment"`
	}{input.APNSTokenHash, input.Challenge, input.Environment})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func decodeAppAttestValue(value string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, ErrInvalidAttestation
}

var _ Attestor = (*AppleAttestor)(nil)
