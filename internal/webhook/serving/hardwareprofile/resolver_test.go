/*
Copyright 2026.

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

package hardwareprofile

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// errClient is a minimal client.Client stub that returns a fixed error from Get.
// The embedded nil interface satisfies all other client.Client methods; only Get is
// overridden. Tests must not call any other method.
type errClient struct {
	client.Client
	err error
}

func (e errClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return e.err
}

// TestResolve_EmptyName verifies that Resolve returns (nil, nil) without calling client.Get
// when the profile name is empty. The embedded errClient would surface an error if Get were
// called, so the absence of an error proves Get was skipped.
func TestResolve_EmptyName(t *testing.T) {
	spy := errClient{err: errors.New("client.Get must not be called when name is empty")}
	profile, err := Resolve(context.Background(), spy, "", "default")
	require.NoError(t, err)
	assert.Nil(t, profile)
}

// TestResolve_FetchError verifies that Resolve wraps and propagates non-404 errors returned
// by client.Get. This path is unreachable from integration tests because
// fake.NewClientBuilder cannot inject arbitrary transient errors without a custom wrapper.
func TestResolve_FetchError(t *testing.T) {
	wantErr := errors.New("transient connection error")
	profile, err := Resolve(context.Background(), errClient{err: wantErr}, "my-hwp", "default")
	require.Error(t, err)
	assert.Nil(t, profile)
	assert.ErrorContains(t, err, "transient connection error")
}
