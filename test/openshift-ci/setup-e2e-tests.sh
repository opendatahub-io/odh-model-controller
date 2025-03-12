#!/usr/bin/env bash
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This is a helper script to run E2E tests on the openshift-ci operator.
# This script assumes to be run inside a container/machine that has
# python pre-installed and the `oc` command available. Additional tooling,
# like kustomize and the mc client are installed by the script if not available.
# The oc CLI is assumed to be configured with the credentials of the
# target cluster. The target cluster is assumed to be a clean cluster.
set -o errexit
set -o nounset
set -o pipefail

: "${SKLEARN_IMAGE:=kserve/sklearnserver:latest}"
: "${KSERVE_CONTROLLER_IMAGE:=quay.io/opendatahub/kserve-controller:latest}"
: "${KSERVE_AGENT_IMAGE:=quay.io/opendatahub/kserve-agent:latest}"
: "${KSERVE_ROUTER_IMAGE:=quay.io/opendatahub/kserve-router:latest}"
: "${STORAGE_INITIALIZER_IMAGE:=quay.io/opendatahub/kserve-storage-initializer:latest}"
: "${ODH_MODEL_CONTROLLER_IMAGE:=quay.io/opendatahub/odh-model-controller:latest}"

echo "SKLEARN_IMAGE=$SKLEARN_IMAGE"
echo "KSERVE_CONTROLLER_IMAGE=$KSERVE_CONTROLLER_IMAGE"
echo "KSERVE_AGENT_IMAGE=$KSERVE_AGENT_IMAGE"
echo "KSERVE_ROUTER_IMAGE=$KSERVE_ROUTER_IMAGE"
echo "STORAGE_INITIALIZER_IMAGE=$STORAGE_INITIALIZER_IMAGE"

# Create directory for installing tooling
# It is assumed that $HOME/.local/bin is in the $PATH
mkdir -p $HOME/.local/bin
MY_PATH=$(dirname "$0")
ODH_MODEL_CTRL_DIR=$MY_PATH/../../

# If Kustomize is not installed, install it
if ! command -v kustomize &>/dev/null; then
  echo "Installing Kustomize"
  curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash -s -- 5.0.1 $HOME/.local/bin
fi

# If minio CLI is not installed, install it
if ! command -v mc &>/dev/null; then
  echo "Installing Minio CLI"
  curl https://dl.min.io/client/mc/release/linux-amd64/mc --create-dirs -o $HOME/.local/bin/mc
  chmod +x $HOME/.local/bin/mc
fi

# Download Kserve
REPO_URL="https://github.com/opendatahub-io/kserve/archive/refs/heads/master.zip"
cd $HOME
mkdir kserve
wget -O kserve.zip "$REPO_URL"
unzip $HOME/kserve.zip -d kserve
mv "kserve/kserve-master"/* "kserve"
rm -rf "kserve/kserve-master"
rm -rf kserve.zip
KSERVE_DIR=$HOME/kserve

echo "Installing KServe Python SDK ..."
pushd $KSERVE_DIR >/dev/null
  .$KSERVE_DIR/test/scripts/gh-actions/setup-poetry.sh
  .$KSERVE_DIR/test/scripts/gh-actions/check-poetry-lockfile.sh
popd
pushd $KSERVE_DIR/python/kserve >/dev/null
  poetry install --with=test --no-interaction
popd

if [ "$1" != "raw" ]; then
  echo "Installing OSSM"
  ./$KSERVE_DIR/test/scripts/openshift-ci/deploy.ossm.sh
  echo "Installing Serverless"
  ./$KSERVE_DIR/test/scripts/openshift-ci/deploy.serverless.sh
fi

echo "Installing KServe with Minio"
kustomize build $KSERVE_DIR/config/overlays/test |
  sed "s|kserve/storage-initializer:latest|${STORAGE_INITIALIZER_IMAGE}|" |
  sed "s|kserve/agent:latest|${KSERVE_AGENT_IMAGE}|" |
  sed "s|kserve/router:latest|${KSERVE_ROUTER_IMAGE}|" |
  sed "s|kserve/kserve-controller:latest|${KSERVE_CONTROLLER_IMAGE}|" |
  oc apply --server-side=true -f -

# Install DSC/DSCI for test. (sometimes there is timing issue when it is under the same kustomization so it is separated)
oc create -f $KSERVE_DIR/config/overlays/test/dsci.yaml
oc create -f $KSERVE_DIR/config/overlays/test/dsc.yaml

# Patch the inferenceservice-config ConfigMap, when running RawDeployment tests
if [ "$1" == "raw" ]; then
  export OPENSHIFT_INGRESS_DOMAIN=$(oc get ingresses.config cluster -o jsonpath='{.spec.domain}')
  oc patch configmap inferenceservice-config -n kserve --patch-file <(cat $KSERVE_DIR/config/overlays/test/configmap/inferenceservice-openshift-ci-raw.yaml | envsubst)
  oc delete pod -n kserve -l control-plane=kserve-controller-manager

  oc patch DataScienceCluster test-dsc --type='json' -p='[{"op": "replace", "path": "/spec/components/kserve/defaultDeploymentMode", "value": "RawDeployment"}]'
else
  export OPENSHIFT_INGRESS_DOMAIN=$(oc get ingresses.config cluster -o jsonpath='{.spec.domain}')
  oc patch configmap inferenceservice-config -n kserve --patch-file <(cat $KSERVE_DIR/config/overlays/test/configmap/inferenceservice-openshift-ci-serverless.yaml | envsubst)
fi


# Determine the odh model-controller image from params.env
# so it can be used in the sed command later to replace with the PR image
source $ODH_MODEL_CTRL_DIR/config/base/params.env
image_to_replace=$odh-model-controller

echo "Installing odh-model-controller dependencies"
# authorino
curl -sL https://raw.githubusercontent.com/Kuadrant/authorino-operator/main/utils/install.sh | sed "s|kubectl|oc|" |
  bash -s -- -v 0.16.0

# kserve-local-gateway
curl https://raw.githubusercontent.com/opendatahub-io/opendatahub-operator/bde4b4e8478b5d03195e2777b9d550922e3cdcbc/components/kserve/resources/servicemesh/routing/istio-kserve-local-gateway.tmpl.yaml |
    sed "s/{{ .ControlPlane.Namespace }}/istio-system/g" |
    oc create -f -

curl https://raw.githubusercontent.com/opendatahub-io/opendatahub-operator/bde4b4e8478b5d03195e2777b9d550922e3cdcbc/components/kserve/resources/servicemesh/routing/kserve-local-gateway-svc.tmpl.yaml |
    sed "s/{{ .ControlPlane.Namespace }}/istio-system/g" |
    oc create -f -

kustomize build $ODH_MODEL_CTRL_DIR/config/base |
  sed "s|${image_to_replace}|${ODH_MODEL_CONTROLLER_IMAGE}|" |
  oc apply --server-side=true -n kserve -f -

oc wait --for=condition=ready pod -l app=odh-model-controller -n kserve --timeout=300s

echo "Add testing models to minio storage ..." # Reference: config/overlays/test/minio/minio-init-job.yaml
oc expose service minio-service -n kserve && sleep 5
MINIO_ROUTE=$(oc get routes -n kserve minio-service -o jsonpath="{.spec.host}")
mc alias set storage http://$MINIO_ROUTE minio minio123

if ! mc ls storage/example-models >/dev/null 2>&1; then
  mc mb storage/example-models
else
  echo "Bucket 'example-models' already exists."
fi

if [[ $(mc ls storage/example-models/sklearn/model.joblib |wc -l) == "1" ]]; then
  echo "Test model exists"
else
  echo "Copy test model"
  curl -L https://storage.googleapis.com/kfserving-examples/models/sklearn/1.0/model/model.joblib -o /tmp/sklearn-model.joblib
  mc cp /tmp/sklearn-model.joblib storage/example-models/sklearn/model.joblib
fi

oc delete route -n kserve minio-service

echo "Prepare CI namespace and install ServingRuntimes"
cat <<EOF | oc apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: kserve-ci-e2e-test
EOF

if [ "$1" != "raw" ]; then
  cat <<EOF | oc apply -f -
apiVersion: maistra.io/v1
kind: ServiceMeshMember
metadata:
  name: default
  namespace: kserve-ci-e2e-test
spec:
  controlPlaneRef:
    namespace: istio-system
    name: basic
EOF
fi

oc apply -f $PROJECT_ROOT/config/overlays/test/minio/minio-user-secret.yaml -n kserve-ci-e2e-test

kustomize build $KSERVE_DIR/config/overlays/test/clusterresources |
  sed 's/ClusterServingRuntime/ServingRuntime/' |
  sed "s|kserve/sklearnserver:latest|${SKLEARN_IMAGE}|" |
  sed "s|kserve/storage-initializer:latest|${STORAGE_INITIALIZER_IMAGE}|" |
  oc apply -n kserve-ci-e2e-test -f -

if [ "$1" != "raw" ]; then
  oc annotate servingruntimes -n kserve-ci-e2e-test --all serving.knative.openshift.io/enablePassthrough=true
fi

# Allow all traffic to the kserve namespace. Without this networkpolicy, webhook will return 500
# error msg: 'http: server gave HTTP response to HTTPS client"}]},"code":500}'
cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-all
  namespace: kserve
spec:
  podSelector: {}
  ingress:
  - {}
  egress:
  - {}
  policyTypes:
  - Ingress
  - Egress
EOF

echo "Setup complete"
