/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package core

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

const (
	dataconnectionStringPath            = "./testdata/secrets/dataconnection-string.yaml"
	storageconfigEncodedPath            = "./testdata/secrets/storageconfig-encoded.yaml"
	storageconfigEncodedUnmanagedPath   = "./testdata/secrets/storageconfig-encoded-unmanaged.yaml"
	storageconfigCertEncodedPath        = "./testdata/secrets/storageconfig-cert-encoded.yaml"
	storageconfigUpdatedCertEncodedPath = "./testdata/secrets/storageconfig-updated-cert-encoded.yaml"
)

var _ = Describe("Secret Controller (StorageConfig controller)", func() {
	Context("when a dataconnection secret that has 'opendatahub.io/managed=true' and 'opendatahub.io/dashboard=true' is created", func() {
		It("should create a storage-config secret", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating dataconnection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storegeconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(compareSecrets(storegeconfigSecret, expectedStorageConfigSecret)).Should((BeTrue()))
		})
	})

	Context("when all dataconnection secret that has 'opendatahub.io/managed=true' and 'opendatahub.io/dashboard=true' are removed", func() {
		It("should delete existing storage-config secret if the secret has 'opendatahub.io/managed=true'", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating dataconnection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storegeconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(storegeconfigSecret).NotTo(BeNil())

			By("deleting the dataconnection secret")
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, dataconnectionStringSecret)).Should(Succeed())

			storegeconfigSecret, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(storegeconfigSecret).To(BeNil())
			Expect(err).To(HaveOccurred())
			Expect(err).To(BeAssignableToTypeOf(&apierrs.StatusError{}))
		})

		It("should not delete existing storage-config secret if the secret has not 'opendatahub.io/managed=true'", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating dataconnection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storegeconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(storegeconfigSecret).NotTo(BeNil())

			By("updating storage-config label opendatahub.io/managed: false")
			err = updateSecretLabel(k8sClient, WorkingNamespace, storageSecretName, "opendatahub.io/managed", "false")
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(k8sClient, WorkingNamespace, storageSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the dataconnection secret")
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, dataconnectionStringSecret)).Should(Succeed())

			storegeconfigSecret, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(storegeconfigSecret).NotTo(BeNil())
		})
	})

	Context("when the annotation 'opendatahub.io/managed=true' in a 'storage-config' secret is set to false", func() {
		It("should be excluded from reconcile target of storageConfig controller ", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating dataconnection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			var storageconfigSecret *corev1.Secret
			_, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			err = updateSecretLabel(k8sClient, WorkingNamespace, storageSecretName, "opendatahub.io/managed", "false")
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(k8sClient, WorkingNamespace, storageSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			err = updateSecretData(k8sClient, WorkingNamespace, storageSecretName, "aws-connection-minio", "unmanaged")
			Expect(err).NotTo(HaveOccurred())

			storageconfigSecret, err = waitForSecret(k8sClient, WorkingNamespace, storageSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedUnmanagedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(compareSecrets(storageconfigSecret, expectedStorageConfigSecret)).Should((BeTrue()))
		})
	})

	Context("when a configmap 'odh-trusted-ca-bundle' exists or updates", func() {
		It("should add/update certificate keys into storage-config secret", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating odh-trusted-ca-bundle configmap")
			odhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(odhtrustedcabundleConfigMapPath, odhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, odhtrustedcacertConfigMap)).Should(Succeed())

			By("creating dataconnection secret")
			err = convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storageconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Check storage-config secret
			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigCertEncodedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(compareSecrets(storageconfigSecret, expectedStorageConfigSecret)).Should((BeTrue()))

			By("updating odh-trusted-ca-bundle configmap")
			updatedOdhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhtrustedcabundleConfigMapUpdatedPath, updatedOdhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Update(ctx, updatedOdhtrustedcacertConfigMap)).Should(Succeed())

			// Delete existing storage-config secret
			// This will be done by kserve_customcacert_controller but for this test, it needs to be delete manully to update the storage-config
			Expect(k8sClient.Delete(ctx, storageconfigSecret)).Should(Succeed())

			// Check updated storage-config secret
			updatedStorageconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 3*time.Second)
			Expect(err).NotTo(HaveOccurred())
			expectedUpdatedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigUpdatedCertEncodedPath, expectedUpdatedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())

			Expect(compareSecrets(updatedStorageconfigSecret, expectedUpdatedStorageConfigSecret)).Should((BeTrue()))
		})
	})

	Context("when a configmap 'odh-trusted-ca-bundle' does not exist", func() {
		It("should not return error", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating data connection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storageconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Check storage-config secret
			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())

			stringMap := make(map[string]string)
			for key, value := range storageconfigSecret.Data {
				stringValue := string(value)
				stringMap[key] = stringValue
			}
			fmt.Printf("storageconfigSecret %s\n", stringMap)
			stringMap1 := make(map[string]string)
			for key, value := range expectedStorageConfigSecret.Data {
				stringValue := string(value)
				stringMap1[key] = stringValue
			}
			Expect(compareSecrets(storageconfigSecret, expectedStorageConfigSecret)).Should((BeTrue()))
		})
	})
})

func updateSecretData(cli client.Client, namespace, secretName string, dataKey string, dataValue string) error {
	secret := &corev1.Secret{}
	err := cli.Get(context.TODO(), client.ObjectKey{Name: secretName, Namespace: namespace}, secret)
	if err != nil {
		log.Printf("Error getting Secret: %v\n", err)
		return err
	}

	// Add the new data to the Secret
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[dataKey] = []byte(dataValue)

	// Save the updated Secret
	err = cli.Update(context.TODO(), secret)
	if err != nil {
		log.Printf("Error updating Secret: %v\n", err)
		return err
	}
	return nil
}

func updateSecretLabel(cli client.Client, namespace, secretName string, labelKey string, labelValue string) error {
	secret := &corev1.Secret{}
	err := cli.Get(context.TODO(), client.ObjectKey{Name: secretName, Namespace: namespace}, secret)
	if err != nil {
		log.Printf("Error getting Secret: %v\n", err)
		return err
	}

	// Update the label
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[labelKey] = labelValue

	// Save the updated Secret
	err = cli.Update(context.TODO(), secret)
	if err != nil {
		log.Printf("Error updating Secret: %v\n", err)
		return err
	}
	log.Printf("Updated Secret label: %s: %s\n", labelKey, labelValue)
	return nil
}

func waitForSecret(cli client.Client, _, _ string, _ int, delay time.Duration) (*corev1.Secret, error) {
	time.Sleep(delay)
	namespace := WorkingNamespace
	secretName := constants.DefaultStorageConfig
	maxTries := 30

	ctx := context.Background()
	secret := &corev1.Secret{}
	for try := 1; try <= maxTries; try++ {
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret)
		if err == nil {
			return secret, nil
		}
		if !apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get secret %s/%s: %v", namespace, secretName, err)
		}

		if try < maxTries {
			time.Sleep(1 * time.Second)
			return nil, err
		}
	}
	return secret, nil
}

// compareSecrets checks if two Secret data are equal, if not return false
func compareSecrets(s1 *corev1.Secret, s2 *corev1.Secret) bool {
	// Two Secret will be equal if the data is identical
	return reflect.DeepEqual(s1.Data, s2.Data)
}
