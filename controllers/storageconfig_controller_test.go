/*

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

package controllers

import (
	"context"
	"log"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mfc "github.com/manifestival/controller-runtime-client"
	mf "github.com/manifestival/manifestival"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	dataconnectionStringPath          = "./testdata/secrets/dataconnection-string.yaml"
	storageconfigEncodedPath          = "./testdata/secrets/storageconfig-encoded.yaml"
	storageconfigEncodedUnmanagedPath = "./testdata/secrets/storageconfig-encoded-unmanaged.yaml"
)

var _ = Describe("StorageConfig controller", func() {
	var opts mf.Option
	ctx := context.Background()
	client := mfc.NewClient(cli)
	opts = mf.UseClient(client)

	Context("when a dataconnection secret that has 'opendatahub.io/managed=true' and 'opendatahub.io/dashboard=true' is created", func() {
		It("should create a storage-config secret", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating dataconnection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storegeconfigSecret, err := waitForSecret(cli, WorkingNamespace, "storage-config", 30*time.Second)
			Expect(err).NotTo(HaveOccurred())

			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedPath, expectedStorageConfigSecret, opts)
			Expect(err).NotTo(HaveOccurred())

			Expect(compareSecrets(storegeconfigSecret, expectedStorageConfigSecret)).Should((BeTrue()))
		})
	})

	Context("when the annotation 'opendatahub.io/managed=true' in a 'storage-config' secret is set to false", func() {
		It("should be excluded from reconcile target of storageConfig controller ", func() {
			dataconnectionStringSecret := &corev1.Secret{}

			By("creating dataconnection secret")
			err := convertToStructuredResource(dataconnectionStringPath, dataconnectionStringSecret, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, dataconnectionStringSecret)).Should(Succeed())

			storageconfigSecret := &corev1.Secret{}
			storageconfigSecret, err = waitForSecret(cli, WorkingNamespace, "storage-config", 30*time.Second)
			Expect(err).NotTo(HaveOccurred())

			err = updateSecretLabel(cli, WorkingNamespace, storageSecretName, "opendatahub.io/managed", "false")
			Expect(err).NotTo(HaveOccurred())
			storageconfigSecret, err = waitForSecret(cli, WorkingNamespace, storageSecretName, 30*time.Second)
			Expect(err).NotTo(HaveOccurred())

			err = updateSecretData(cli, WorkingNamespace, storageSecretName, "aws-connection-minio", "unmanaged")
			Expect(err).NotTo(HaveOccurred())

			storageconfigSecret, err = waitForSecret(cli, WorkingNamespace, storageSecretName, 30*time.Second)
			Expect(err).NotTo(HaveOccurred())

			expectedStorageConfigSecret := &corev1.Secret{}
			err = convertToStructuredResource(storageconfigEncodedUnmanagedPath, expectedStorageConfigSecret, opts)
			Expect(err).NotTo(HaveOccurred())

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
	return nil
}

func waitForSecret(cli client.Client, namespace, secretName string, timeout time.Duration) (*corev1.Secret, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		secret := &corev1.Secret{}
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret)
		if err != nil {
			log.Printf("Waiting for Secret %s/%s to be created: %v", namespace, secretName, err)
			time.Sleep(1 * time.Second)
			continue
		}
		log.Printf("Secret %s/%s is created", secret.Namespace, secret.Name)

		return secret, nil
	}
}

// compareSecrets checks if two Secret data are equal, if not return false
func compareSecrets(s1 *corev1.Secret, s2 *corev1.Secret) bool {
	// Two Secret will be equal if the data is identical
	return reflect.DeepEqual(s1.Data, s2.Data)
}
