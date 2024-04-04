package utils

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	//nolint:golint,revive
	. "github.com/onsi/ginkgo"
	"gopkg.in/yaml.v2"
)

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) ([]byte, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir
	fmt.Fprintf(GinkgoWriter, "running dir: %s\n", cmd.Dir)

	// To allow make commands be executed from the project directory which is subdir on SDK repo
	// TODO:(user) You might not need the following code
	if err := os.Chdir(cmd.Dir); err != nil {
		fmt.Fprintf(GinkgoWriter, "chdir dir: %s\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	fmt.Fprintf(GinkgoWriter, "running: %s\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed with error: (%v) %s", command, err, string(output))
	}

	return output, nil
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, err
	}
	wd = strings.Replace(wd, "/test/e2e", "", -1)
	return wd, nil
}

func ConvertToStructuredResource(path string, out interface{}) (err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Unmarshal the YAML data into the struct
	err = yaml.Unmarshal(data, out)
	if err != nil {
		return err
	}
	return nil
}

// InstallModelControllerOperator build and deploy the odh model controller
// Assume the docker image has been already built
func InstallModelControllerOperator() (err error) {
	cmd := exec.Command("make", "install")
	_, err = Run(cmd)
	if err != nil {
		return
	}

	cmd = exec.Command("make", "deploy")
	_, err = Run(cmd)
	return
}

// UninstallModelControllerOperator undeploy the odh model controller
func UninstallModelControllerOperator() (err error) {
	cmd := exec.Command("make", "undeploy")
	_, err = Run(cmd)
	if err != nil {
		return
	}

	cmd = exec.Command("make", "uninstall")
	_, err = Run(cmd)
	return
}
