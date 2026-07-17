package platform

import "os"

const (
	// PlatformTypeEnv is the environment variable used to select the deployment platform.
	PlatformTypeEnv = "ODH_PLATFORM_TYPE"
	// XKS is the platform type for vanilla Kubernetes (extended KServe) deployments.
	XKS = "xKS"
)

// IsXKS reports whether the controller should run in xKS webhook-only mode.
func IsXKS() bool {
	return os.Getenv(PlatformTypeEnv) == XKS
}
