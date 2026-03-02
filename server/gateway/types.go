package gateway

// Well-known annotations used to expose human-readable metadata.
const (
	AnnotationDisplayName = "openshift.io/display-name"
	AnnotationDescription = "openshift.io/description"
)

// GatewayRef represents a single usable gateway+listener pair.
type GatewayRef struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Listener    string `json:"listener"`
	Status      string `json:"status"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

// GatewaysResponse is the API response envelope.
type GatewaysResponse struct {
	Gateways []GatewayRef `json:"gateways"`
}

// ErrorResponse is returned for 4xx/5xx errors.
type ErrorResponse struct {
	Error string `json:"error"`
}