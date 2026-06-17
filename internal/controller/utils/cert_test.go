package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createDummyCACertAndKey() (*corev1.Secret, error) {
	// Generate a dummy CA private key
	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// Create a dummy CA certificate template
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Dummy CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Create the dummy CA certificate
	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caPrivateKey.PublicKey, caPrivateKey)
	if err != nil {
		return nil, err
	}

	// Encode CA certificate and private key to PEM format
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	caPrivateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caPrivateKey)})

	// Create a Secret object to store the CA certificate and private key
	caCertSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy-ca-secret",
			Namespace: "test-namespace",
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       caCertPEM,
			corev1.TLSPrivateKeyKey: caPrivateKeyPEM,
		},
	}

	return caCertSecret, nil
}

func TestGenerateCertificate(t *testing.T) {
	// Create a dummy CA certificate and private key
	caCertSecret, err := createDummyCACertAndKey()
	assert.NoError(t, err, "Failed to create dummy CA certificate and key")

	// Define SAN IPs and DNS names for the test
	sanIPs := []string{"192.168.1.1", "10.0.0.1"}
	sanDNSs := []string{"example.com", "test.local"}

	// Call the function under test
	cert, key, err := generateCertificate(sanIPs, sanDNSs, caCertSecret)

	// Assert no error occurred
	assert.NoError(t, err, "generateCertificate should not return an error")

	// Assert certificate and key are not nil
	assert.NotNil(t, cert, "Certificate should not be nil")
	assert.NotNil(t, key, "Private key should not be nil")

	// Decode and parse the generated certificate
	block, _ := pem.Decode(cert)
	assert.NotNil(t, block, "Failed to decode PEM block from certificate")
	parsedCert, err := x509.ParseCertificate(block.Bytes)
	assert.NoError(t, err, "Failed to parse certificate")

	// Assert the SAN IPs are present in the certificate
	for _, sanIP := range sanIPs {
		ip := net.ParseIP(sanIP)
		found := false
		for _, certIP := range parsedCert.IPAddresses {
			if certIP.Equal(ip) { // Use Equal to handle IPv4/IPv6 comparison
				found = true
				break
			}
		}
		assert.True(t, found, "Certificate should contain SAN IP: %s", sanIP)
	}

	// Assert the SAN DNS names are present in the certificate
	for _, dns := range sanDNSs {
		assert.Contains(t, parsedCert.DNSNames, dns, "Certificate should contain SAN DNS")
	}

	// Decode and parse the private key
	keyBlock, _ := pem.Decode(key)
	assert.NotNil(t, keyBlock, "Failed to decode PEM block from private key")
	_, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	assert.NoError(t, err, "Failed to parse private key")
}

func TestGenerateCACertificate(t *testing.T) {
	testCases := []struct {
		name        string
		addr        string
		expectedIPs []net.IP
		expectedDNS []string
	}{
		{
			name:        "Test with valid IP address",
			addr:        "192.168.1.1",
			expectedIPs: []net.IP{net.ParseIP("192.168.1.1")},
			expectedDNS: []string{},
		},
		{
			name:        "Test with wildcard DNS address",
			addr:        "*.example.com",
			expectedIPs: nil,
			expectedDNS: []string{"*.example.com"},
		},
		{
			name:        "Test with regular DNS address",
			addr:        "test.local",
			expectedIPs: nil,
			expectedDNS: []string{"test.local"},
		},
		{
			name:        "Test with empty address",
			addr:        "",
			expectedIPs: nil,
			expectedDNS: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate the certificate and key
			certBytes, keyBytes, err := generateCACertificate(tc.addr)

			// Assert no errors during certificate generation
			assert.NoError(t, err, "generateCACertificate should not return an error")
			assert.NotNil(t, certBytes, "Certificate should not be nil")
			assert.NotNil(t, keyBytes, "Private key should not be nil")

			// Decode and parse the certificate
			block, _ := pem.Decode(certBytes)
			assert.NotNil(t, block, "Failed to decode PEM block from certificate")
			parsedCert, err := x509.ParseCertificate(block.Bytes)
			assert.NoError(t, err, "Failed to parse certificate")

			// Check if the certificate is a CA
			assert.True(t, parsedCert.IsCA, "Certificate should be a CA")

			// Ensure the certificate contains the CertSign KeyUsage
			assert.True(t, parsedCert.KeyUsage&x509.KeyUsageCertSign != 0, "Certificate should have CertSign KeyUsage")

			// Check if the expected IPs are present in the SAN
			for _, expectedIP := range tc.expectedIPs {
				found := false
				for _, certIP := range parsedCert.IPAddresses {
					if certIP.Equal(expectedIP) {
						found = true
						break
					}
				}
				assert.True(t, found, "Certificate should contain SAN IP: %s", expectedIP)
			}

			// Check if the expected DNS names are present in the SAN
			for _, expectedDNS := range tc.expectedDNS {
				assert.Contains(t, parsedCert.DNSNames, expectedDNS, "Certificate should contain SAN DNS: %s", expectedDNS)
			}

			// Decode and parse the private key
			keyBlock, _ := pem.Decode(keyBytes)
			assert.NotNil(t, keyBlock, "Failed to decode PEM block from private key")
			_, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
			assert.NoError(t, err, "Failed to parse private key")
		})
	}
}

// Helper function to generate a certificate with a specific expiration date
func generateTestCert(expirationDate time.Time) ([]byte, error) {
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now(),
		NotAfter:              expirationDate, // Expiration date
		BasicConstraintsValid: true,
	}

	// Generate a private key
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// Self-sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	// Encode to PEM format
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return certPEM, nil
}

func TestIsCertExpired(t *testing.T) {
	fixedNow := time.Now()

	tests := []struct {
		name       string
		beforeYear int
		expiryDate time.Time
		expect     bool
	}{
		{
			name:       "return true if certificate expire date has less than beforeYear(2 years)",
			beforeYear: 2,
			expiryDate: fixedNow.AddDate(1, 0, 0), // today + 1 year
			expect:     true,
		},
		{
			name:       "return false if certificate expire date has more than beforeYear(2 years)",
			beforeYear: 2,
			expiryDate: fixedNow.AddDate(3, 0, 0), // today + 3 years
			expect:     false,
		},
		{
			name:       "return true if certficate is already expired",
			beforeYear: 0,
			expiryDate: fixedNow.AddDate(-1, 0, 0), // today - 1 years
			expect:     true,
		},
		{
			name:       "return true if certificate valid exactly at the threshold",
			beforeYear: 2,
			expiryDate: fixedNow.AddDate(2, 0, 0), // today + 2 years exactly
			expect:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert, err := generateTestCert(tt.expiryDate)
			if err != nil {
				t.Fatalf("failed to generate test certificate: %v", err)
			}

			result, err := isCertExpired(cert, tt.beforeYear)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, result)
			}
		})
	}
}
