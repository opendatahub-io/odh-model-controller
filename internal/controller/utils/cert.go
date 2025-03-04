package utils

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateSelfSignedCACertificate(ctx context.Context, c client.Client, secretName, domain, namespace string) error {
	certSecret, err := GenerateSelfSignedCACertificateAsSecret(ctx, secretName, domain, namespace)
	if err != nil {
		return fmt.Errorf("failed generating self-signed ca certificate: %w", err)
	}

	if errGen := generateCertSecret(ctx, c, certSecret); errGen != nil {
		return fmt.Errorf("failed update self-signed ca certificate secret: %w", errGen)
	}

	return nil
}

func GenerateSelfSignedCACertificateAsSecret(ctx context.Context, name, addr, namespace string) (*corev1.Secret, error) {
	cert, key, err := generateCACertificate(addr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "self-signed-cert",
				"app.kubernetes.io/component":  "odh-model-serving",
				"app.kubernetes.io/part-of":    "kserve",
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

func generateCACertificate(addr string) ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
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
			CommonName:   "self-signed-ca-cert",
			Organization: []string{"opendatahub-self-signed"},
		},
		NotBefore:             now.UTC(),
		NotAfter:              now.Add(time.Second * 60 * 60 * 24 * 365 * 5).UTC(),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	if addr != "" {
		if ip := net.ParseIP(addr); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			if strings.HasPrefix(addr, "*.") {
				tmpl.DNSNames = append(tmpl.DNSNames, addr[2:])
			}
			tmpl.DNSNames = append(tmpl.DNSNames, addr)
		}

		tmpl.DNSNames = append(tmpl.DNSNames, "localhost")

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
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}); err != nil {
		return nil, nil, errors.WithStack(err)
	}

	return certBuffer.Bytes(), keyBuffer.Bytes(), nil
}

func CreateSelfSignedCertificate(ctx context.Context, c client.Client, caSecretName, caSecretNamespace, targetSecretName, targetNamespace string, sanIPs, sanDNSs []string) (*corev1.Secret, error) {
	caCertSecret := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Namespace: caSecretNamespace, Name: caSecretName}, caCertSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get ca secret:%w", err)
	}

	certSecret, err := GenerateSelfSignedCertificateAsSecret(caCertSecret, targetSecretName, targetNamespace, sanIPs, sanDNSs)
	if err != nil {
		return nil, fmt.Errorf("failed generating self-signed certificate: %w", err)
	}

	if errGen := generateCertSecret(ctx, c, certSecret); errGen != nil {
		return nil, fmt.Errorf("failed update self-signed certificate secret: %w", errGen)
	}

	return certSecret, nil
}

func GenerateSelfSignedCertificateAsSecret(caCertSecret *corev1.Secret, targetSecretName, targetNamespace string, sanIPs, sanDNSs []string) (*corev1.Secret, error) {
	cert, key, err := generateCertificate(sanIPs, sanDNSs, caCertSecret)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetSecretName,
			Namespace: targetNamespace,
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
		},
		Type: corev1.SecretTypeTLS,
	}, nil
}

func generateCertificate(sanIPs, sanDNSs []string, caCertSecret *corev1.Secret) ([]byte, []byte, error) {
	stripedCert, err := removePEMLines(caCertSecret.Data[corev1.TLSCertKey])
	if err != nil {
		return nil, nil, err
	}
	caCert, err := x509.ParseCertificate(stripedCert)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	stripedPrivateKey, err := removePEMLines(caCertSecret.Data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return nil, nil, err
	}

	caPrivateKey, err := x509.ParsePKCS1PrivateKey(stripedPrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 4096)
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
			CommonName:   "self-signed-cert",
			Organization: []string{"opendatahub-self-signed"},
		},
		NotBefore:             now.UTC(),
		NotAfter:              now.Add(time.Second * 60 * 60 * 24 * 365 * 3).UTC(),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageDataEncipherment,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	for _, sanIP := range sanIPs {
		if ip := net.ParseIP(sanIP); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		}
	}
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"))

	sanDNSs = append(sanDNSs, "localhost")
	tmpl.DNSNames = append(tmpl.DNSNames, sanDNSs...)

	certDERBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, key.Public(), caPrivateKey)
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
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	if err := pem.Encode(&keyBuffer, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
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
		// check existing cert is expired
		certExpired, err := isCertExpired(existingSecret.Data[corev1.TLSCertKey], 2)
		if err != nil {
			return err
		}

		// Secret exists but with a different type, delete and create it again
		if existingSecret.Type != certSecret.Type || certExpired {
			return recreateSecret(ctx, c, existingSecret, certSecret)
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

func isCertExpired(cert []byte, beforeYear int) (bool, error) {
	isExpired := false
	stripedCert, err := removePEMLines(cert)
	if err != nil {
		return false, err
	}

	parsedCert, err := x509.ParseCertificate(stripedCert)
	if err != nil {
		return false, fmt.Errorf("failed to parse certificate: %v", err)
	}
	currentTime := time.Now()

	if currentTime.Equal(parsedCert.NotAfter) || currentTime.After(parsedCert.NotAfter) {
		isExpired = true
	}

	if !isExpired && beforeYear != 0 {
		expirationThreshold := parsedCert.NotAfter.AddDate(-beforeYear, 0, 0)
		if currentTime.After(expirationThreshold) || currentTime.Equal(expirationThreshold) {
			isExpired = true
		}
	}
	return isExpired, nil
}

func removePEMLines(cert []byte) ([]byte, error) {
	var result []string
	lines := strings.Split(string(cert), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "-----BEGIN") && !strings.HasPrefix(line, "-----END") {
			result = append(result, line)
		}
	}
	certData := strings.Join(result, "\n")
	decodedCert, err := base64.StdEncoding.DecodeString(certData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode certificate: %v", err)
	}
	return decodedCert, nil
}
