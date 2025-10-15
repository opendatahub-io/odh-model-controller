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

var _ = Describe("Secret Controller (StorageConfig controller)", func() {
	Context("when a dataconnection secret that has 'opendatahub.io/managed=true' and 'opendatahub.io/dashboard=true' is created", func() {
		It("should create a storage-config secret", func() {
			By("creating dataconnection secrets")
			dataconnectionHttpStringSecret := &corev1.Secret{}
			err := convertToStructuredResource(dataconnectionHttpStringPath, dataconnectionHttpStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			dataconnectionHttpsStringSecret := &corev1.Secret{}
			err = convertToStructuredResource(dataconnectionHttpsStringPath, dataconnectionHttpsStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			storegeconfigSecret, err := waitForStorageConfigWithEntries(k8sClient, WorkingNamespace, 2, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(compareSecrets(storegeconfigSecret, expectedStorageConfigSecret)).Should(BeTrue())
		})
	})

	Context("when all dataconnection secrets that have 'opendatahub.io/managed=true' and 'opendatahub.io/dashboard=true' labels are removed", func() {
		It("should delete existing storage-config secret if the secret has 'opendatahub.io/managed=true' label", func() {
			By("creating dataconnection secrets")
			dataconnectionHttpStringSecret := &corev1.Secret{}
			err := convertToStructuredResource(dataconnectionHttpStringPath, dataconnectionHttpStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			dataconnectionHttpsStringSecret := &corev1.Secret{}
			err = convertToStructuredResource(dataconnectionHttpsStringPath, dataconnectionHttpsStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			_, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the dataconnection secrets")
			Expect(k8sClient.Delete(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			err = waitForSecretDeletion(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not delete existing storage-config secret if the secret does not have 'opendatahub.io/managed=true' label", func() {
			By("creating dataconnection secrets")
			dataconnectionHttpStringSecret := &corev1.Secret{}
			err := convertToStructuredResource(dataconnectionHttpStringPath, dataconnectionHttpStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			dataconnectionHttpsStringSecret := &corev1.Secret{}
			err = convertToStructuredResource(dataconnectionHttpsStringPath, dataconnectionHttpsStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			_, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("updating storage-config label opendatahub.io/managed: false")
			err = updateSecretLabel(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, "opendatahub.io/managed", "false")
			Expect(err).NotTo(HaveOccurred())

			By("deleting the dataconnection secrets")
			Expect(k8sClient.Delete(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			_, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 3*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when the label 'opendatahub.io/managed=true' in a 'storage-config' secret is set to false", func() {
		It("should be excluded from reconcile target of storageConfig controller ", func() {
			By("creating dataconnection secrets")
			dataconnectionHttpStringSecret := &corev1.Secret{}
			err := convertToStructuredResource(dataconnectionHttpStringPath, dataconnectionHttpStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			dataconnectionHttpsStringSecret := &corev1.Secret{}
			err = convertToStructuredResource(dataconnectionHttpsStringPath, dataconnectionHttpsStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			_, err = waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("updating storage-config label opendatahub.io/managed: false")
			err = updateSecretLabel(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, "opendatahub.io/managed", "false")
			Expect(err).NotTo(HaveOccurred())

			By("updating storage-config secret data")
			err = updateSecretData(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, "aws-connection-minio-http", "unmanaged")
			Expect(err).NotTo(HaveOccurred())
			err = updateSecretData(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, "aws-connection-minio-https", "unmanaged")
			Expect(err).NotTo(HaveOccurred())

			storageconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 3*time.Second)
			Expect(err).NotTo(HaveOccurred())

			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedUnmanagedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(compareSecrets(storageconfigSecret, expectedStorageConfigSecret)).Should(BeTrue())
		})
	})

	Context("when a configmap 'odh-kserve-custom-ca-bundle' exists or updates", func() {
		It("should add/update certificate keys into storage-config secret if the endpoint is https", func() {
			By("creating the odh-kserve-custom-ca-bundle configmap")
			odhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(odhtrustedcabundleConfigMapPath, odhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, odhtrustedcacertConfigMap)).Should(Succeed())
			openshiftServiceCAConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(openshiftServiceCAConfigMapPath, openshiftServiceCAConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, openshiftServiceCAConfigMap)).Should(Succeed())

			_, err = waitForConfigMap(k8sClient, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("creating dataconnection secrets")
			dataconnectionHttpStringSecret := &corev1.Secret{}
			err = convertToStructuredResource(dataconnectionHttpStringPath, dataconnectionHttpStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpStringSecret)).Should(Succeed())
			dataconnectionHttpsStringSecret := &corev1.Secret{}
			err = convertToStructuredResource(dataconnectionHttpsStringPath, dataconnectionHttpsStringSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, dataconnectionHttpsStringSecret)).Should(Succeed())

			// Check storage-config secret
			storageconfigSecret, err := waitForStorageConfigWithEntries(k8sClient, WorkingNamespace, 2, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigCertEncodedPath, expectedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())

			Expect(compareSecrets(storageconfigSecret, expectedStorageConfigSecret)).Should(BeTrue())

			By("updating odh-kserve-custom-ca-bundle configmap")
			updatedOdhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhtrustedcabundleConfigMapUpdatedPath, updatedOdhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Update(ctx, updatedOdhtrustedcacertConfigMap)).Should(Succeed())
			updatedOpenshiftServiceCAConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(openshiftServiceCAConfigMapUpdatedPath, updatedOpenshiftServiceCAConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Update(ctx, updatedOpenshiftServiceCAConfigMap)).Should(Succeed())

			// Check updated storage-config secret
			updatedStorageconfigSecret, err := waitForSecret(k8sClient, WorkingNamespace, constants.DefaultStorageConfig, 30, 3*time.Second)
			Expect(err).NotTo(HaveOccurred())
			expectedUpdatedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigCertEncodedUpdatedPath, expectedUpdatedStorageConfigSecret)
			Expect(err).NotTo(HaveOccurred())

			Expect(compareSecrets(updatedStorageconfigSecret, expectedUpdatedStorageConfigSecret)).Should(BeTrue())
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

// nolint:unparam
func waitForSecret(cli client.Client, namespace string, secretName string, maxTries int, delay time.Duration) (*corev1.Secret, error) {
	ctx := context.Background()
	secret := &corev1.Secret{}
	var err error
	for try := 1; try <= maxTries; try++ {
		err = cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret)
		if err == nil {
			return secret, nil
		}
		if !apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get secret %s/%s: %v", namespace, secretName, err)
		}
		// Sleep between retries
		time.Sleep(delay)
	}
	return nil, err
}

// waitForStorageConfigWithEntries waits for storage-config secret to exist and have the expected number of data entries
func waitForStorageConfigWithEntries(cli client.Client, namespace string, expectedEntries int, maxTries int, delay time.Duration) (*corev1.Secret, error) {
	ctx := context.Background()
	secret := &corev1.Secret{}
	var err error
	for try := 1; try <= maxTries; try++ {
		err = cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: constants.DefaultStorageConfig}, secret)
		if err == nil && len(secret.Data) == expectedEntries {
			return secret, nil
		}
		if err != nil && !apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get secret %s/%s: %v", namespace, constants.DefaultStorageConfig, err)
		}
		// Sleep between retries
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("storage-config secret does not have expected %d entries after %d tries", expectedEntries, maxTries)
}

// waitForSecretDeletion waits for a secret to be deleted (not found)
func waitForSecretDeletion(cli client.Client, namespace string, secretName string, maxTries int, delay time.Duration) error {
	ctx := context.Background()
	secret := &corev1.Secret{}
	var err error
	for try := 1; try <= maxTries; try++ {
		err = cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret)
		if apierrs.IsNotFound(err) {
			return nil // Secret is deleted, success
		}
		if err != nil {
			return fmt.Errorf("unexpected error checking secret %s/%s: %v", namespace, secretName, err)
		}
		// Secret still exists, wait and try again
		time.Sleep(delay)
	}
	return fmt.Errorf("secret %s/%s was not deleted after %d tries", namespace, secretName, maxTries)
}

// compareSecrets checks if two Secret data are equal, if not return false
func compareSecrets(s1 *corev1.Secret, s2 *corev1.Secret) bool {
	// Two Secret will be equal if the data is identical
	return reflect.DeepEqual(s1.Data, s2.Data)
}
