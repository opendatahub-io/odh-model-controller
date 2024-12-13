package utils

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/hashicorp/go-multierror"
)

const (
	targetFileFmt = "%s/ngc_cat_resp__rt%d__sz%d__pg%d.json"
	targetFolder  = "../../../hack/benchmarks/documents"
)

func TestNIMBenchmarks(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NIM Benchmarks")
}

type NimHttpClientBenchmarksMock struct {
	NumRuntimes, PageSize int
}

func (r *NimHttpClientBenchmarksMock) Do(req *http.Request) (*http.Response, error) {
	catParams := &NimCatalogQuery{}
	jErr := json.Unmarshal([]byte(req.URL.Query().Get("q")), catParams)
	if jErr != nil {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(bytes.NewReader([]byte(jErr.Error())))}, nil
	}
	f, fErr := os.ReadFile(fmt.Sprintf(targetFileFmt, targetFolder, r.NumRuntimes, r.PageSize, catParams.Page))
	if fErr != nil {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(bytes.NewReader([]byte(fErr.Error())))}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f))}, nil
}

// we generate documents for benchmarking with hack/benchmarks/generate_nim_benchmark_documents.go
// `make benchmarks` will run the benchmarks and create the documents if target the folder doesn't exist
// `make nim_benchmark_documents` will regenerate the documents
var _ = Describe("Benchmark NIM catalog unmarshalling", func() {
	Measure("Measure with responses with various page sizes", func(b Benchmarker) {
		// go run hack/benchmarks/generate_nim_benchmark_documents.go -runtimes=1000 -size=100
		Expect(runBenchmarks(b, "Measure 1000 models with 100 page size (10 pages)", 1000, 100)).To(Succeed())
		// go run hack/benchmarks/generate_nim_benchmark_documents.go -runtimes=1000 -size=1000
		Expect(runBenchmarks(b, "Measure 1000 models with 1000 page size (1 page)", 1000, 1000)).To(Succeed())
	}, 2000)
})

func runBenchmarks(benchmarker Benchmarker, title string, numRuntimes, pageSize int) error {
	errs := &multierror.Error{}
	NimHttpClient = &NimHttpClientBenchmarksMock{numRuntimes, pageSize}
	benchmarker.Time(title, func() {
		_, err := getNimRuntimes([]NimRuntime{}, 0, pageSize)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	})
	return errs.ErrorOrNil()
}
