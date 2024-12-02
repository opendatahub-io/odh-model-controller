package utils

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateSelfSignedCertificate(ctx context.Context, c client.Client, secretName, domain, namespace string) error {
	certSecret, err := GenerateSelfSignedCertificateAsSecret(secretName, domain, namespace)
	if err != nil {
		return fmt.Errorf("failed generating self-signed certificate: %w", err)
	}

	if errGen := generateCertSecret(ctx, c, certSecret); errGen != nil {
		return fmt.Errorf("failed update self-signed certificate secret: %w", errGen)
	}

	return nil
}

func GenerateSelfSignedCertificateAsSecret(name, addr, namespace string) (*corev1.Secret, error) {
	cert, key, err := generateCertificate(addr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "odh-model-controller",
				"app.kubernetes.io/component":  "kserve",
				"app.kubernetes.io/part-of":    "odh-model-serving",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
		},
		Type: corev1.SecretTypeTLS,
	}, nil
}

func generateCertificate(addr string) ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	seededRand, cryptErr := rand.Int(rand.Reader, big.NewInt(time.Now().UnixNano()))
	if cryptErr != nil {
		return nil, nil, errors.WithStack(cryptErr)
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: seededRand,
		Subject: pkix.Name{
			CommonName:   addr,
			Organization: []string{"serving-self-signed"},
		},
		NotBefore:             now.UTC(),
		NotAfter:              now.Add(time.Second * 60 * 60 * 24 * 365 * 10).UTC(),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDERBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}
	certificate, err := x509.ParseCertificate(certDERBytes)
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	certBuffer := bytes.Buffer{}
	if err := pem.Encode(&certBuffer, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate.Raw,
	}); err != nil {
		return nil, nil, errors.WithStack(err)
	}

	keyBuffer := bytes.Buffer{}
	if err := pem.Encode(&keyBuffer, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}); err != nil {
		return nil, nil, errors.WithStack(err)
	}

	return certBuffer.Bytes(), keyBuffer.Bytes(), nil
}

// recreateSecret deletes the existing secret and creates a new one.
func recreateSecret(ctx context.Context, c client.Client, existingSecret, newSecret *corev1.Secret) error {
	if err := c.Delete(ctx, existingSecret); err != nil {
		return fmt.Errorf("failed to delete existing secret before recreating new one: %w", err)
	}
	if err := c.Create(ctx, newSecret); err != nil {
		return fmt.Errorf("failed to create new secret after existing one has been deleted: %w", err)
	}
	return nil
}

// generateCertSecret creates a secret if it does not exist; recreate this secret if type not match; update data if outdated.
func generateCertSecret(ctx context.Context, c client.Client, certSecret *corev1.Secret) error {
	existingSecret := &corev1.Secret{}
	errGet := c.Get(ctx, client.ObjectKeyFromObject(certSecret), existingSecret)
	switch {
	case errGet == nil:
		// Secret exists but with a different type, delete and create it again
		if existingSecret.Type != certSecret.Type {
			return recreateSecret(ctx, c, existingSecret, certSecret)
		}
		// update data if found with same type but outdated content
		if isSecretOutdated(existingSecret.Data, certSecret.Data) {
			if errUpdate := c.Update(ctx, certSecret); errUpdate != nil {
				return fmt.Errorf("failed to update existing secret: %w", errUpdate)
			}
		}
	case k8serr.IsNotFound(errGet):
		// Secret does not exist, create it
		if errCreate := c.Create(ctx, certSecret); errCreate != nil {
			return fmt.Errorf("failed creating new certificate secret: %w", errCreate)
		}
	default:
		return fmt.Errorf("failed getting certificate secret: %w", errGet)
	}

	return nil
}

// isSecretOutdated compares two secret data of type map[string][]byte and returns true if they are not equal.
func isSecretOutdated(existingSecretData, newSecretData map[string][]byte) bool {
	if len(existingSecretData) != len(newSecretData) {
		return true
	}

	for key, value1 := range existingSecretData {
		value2, ok := newSecretData[key]
		if !ok {
			return true
		}
		if !bytes.Equal(value1, value2) {
			return true
		}
	}

	return false
}
