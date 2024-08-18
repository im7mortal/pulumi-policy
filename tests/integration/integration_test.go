// Copyright 2016-2021, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integrationtests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ptesting "github.com/pulumi/pulumi/sdk/v3/go/common/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Runtime string

const (
	NodeJS Runtime = "nodejs"
	Python Runtime = "python"
)

func abortIfFailed(t *testing.T) {
	if t.Failed() {
		t.Fatal("Aborting test as a result of unrecoverable error.")
	}
}

type PolicyConfig map[string]interface{}

// policyTestScenario describes an iteration of the
type policyTestScenario struct {
	// WantErrors is the error message we expect to see in the command's output.
	WantErrors []string
	// Whether the error messages are advisory, and don't actually fail the operation.
	Advisory bool
	// The Policy Pack configuration to use for the test scenario.
	PolicyPackConfig map[string]PolicyConfig
	// Name of the scenario. Generated on the fly.
	Name string
}

func pathEnvWith(path string) string {
	pathEnv, pathSeparator := os.Getenv("PATH"), ":"
	if runtime.GOOS == "windows" {
		pathSeparator = ";"
	}
	return "PATH=" + pathEnv + pathSeparator + path
}

// getCmdArgs generates the command and arguments to execute the Pulumi operation.
// For now (and possibly only), it differentiates Python because it requires pipenv
func getCmdArgs(isPython bool, policyPackDirectoryPath string) (string, []string) {
	cmd := "pulumi"
	args := []string{"up", "--yes", "--policy-pack", policyPackDirectoryPath}
	if isPython {
		cmd = "pipenv"
		args = append([]string{"run", "pulumi"}, args...)
	}
	return cmd, args
}

type Workflow func(ctx context.Context)

// PolicyTestCase is the main struct that encapsulates all the data and functions required to execute a policy pack test.
type PolicyTestCase struct {
	t             *testing.T           // The test instance.
	testDirName   string               // The directory containing the policies and configs.
	runtime       Runtime              // The runtime being tested (e.g., NodeJS, Python etc).
	initialConfig map[string]string    // Initial configuration settings for Pulumi.
	scenarios     []policyTestScenario // List of scenarios to run within this test case.

	programDir string // Directory containing the Pulumi program.
	stackName  string // Name of the Pulumi stack being tested.

	pythonVenv    sync.Once // Ensures Python virtual environment setup runs only once.
	hasPythonPack bool      // Whether a Python policy pack is included in the test case.

	e *ptesting.Environment // Environment object used to manage the testing workspace.

	workflows []Workflow // A list of  installation workflows.

	policyRuntimesQueue chan string   // A queue of policies to test
	policyRuntimesDone  chan struct{} // Channel indicating when all policies were tested.

	// Counter for the number of policy runtimes.
	// Used to determine if all policies have been tested.
	numOfPolicyRuntimes atomic.Int32
}

func NewCase(
	t *testing.T, testDirName string, runtime Runtime,
	initialConfig map[string]string, scenarios []policyTestScenario,
) {
	p := PolicyTestCase{
		t:                   t,
		testDirName:         testDirName,
		runtime:             runtime,
		initialConfig:       initialConfig,
		scenarios:           scenarios,
		policyRuntimesQueue: make(chan string, 5),
		policyRuntimesDone:  make(chan struct{}),
	}
	p.Run()
}

func GetDirectories(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}

	return directories, nil
}

func Contains(slice []string, target string) bool {
	for _, item := range slice {
		if item == target {
			return true
		}
	}
	return false
}

// IsPolicyRuntimePicked checks if the runtime is selected based on the POLICY_RUNTIMES environment variable.
// If no environment variable is set, all runtimes are picked by default.
func (p *PolicyTestCase) IsPolicyRuntimePicked(language Runtime) bool {
	env := os.Getenv("POLICY_RUNTIMES")
	// if env not set than run all runtimes
	if env == "" {
		return true
	}
	env = strings.ToLower(env)
	values := strings.Split(env, ",")

	for _, val := range values {
		if Runtime(val) == language {
			return true
		}
	}
	return false
}

// InstallDependency schedules the execution of a dependency installation task in the workflow.
// It will send signal to queue that policy can be runed after it finishes installation
func (p *PolicyTestCase) InstallDependency(f func(ctx context.Context), path string) func(ctx context.Context) {
	return func(ctx context.Context) {
		f(ctx)
		// send signal that policy can be tested
		p.policyRuntimesQueue <- path
	}
}

// MainWorkflowByRuntime ensure that policy installation will be in the same workflow as program
// it they both use the same runtime language
func (p *PolicyTestCase) MainWorkflowByRuntime(r Runtime, f func(ctx context.Context)) func(ctx context.Context) {
	if p.runtime == r {
		return f
	}
	return func(ctx context.Context) {}
}

// CreateWorkflow chains multiple functions together to create a single workflow.
func CreateWorkflow(f ...func(ctx context.Context)) func(ctx context.Context) {
	return func(ctx context.Context) {
		for _, f_ := range f {
			f_(ctx)
		}
	}
}

// ConstructTestWorkflow discovers the necessary test components (such as policy packs) within the test directory
// and sets up the installation workflows
func (p *PolicyTestCase) ConstructTestWorkflow() {

	dirs, err := GetDirectories(p.testDirName)
	if err != nil {
		p.t.Fatalf("Error listing working directory")
	}

	mainProgramWorkflow := CreateWorkflow(
		p.CreateStack,
		p.InstallProgram,
		p.TestComponentInstallation(dirs),
		p.RunScenarios,
	)

	p.workflows = []Workflow{
		CreateWorkflow(
			p.NodeJSPolicyWorkflow(dirs)), // NodeJS policy library must be installed before program so it could be linked
		p.MainWorkflowByRuntime(NodeJS, mainProgramWorkflow),
		CreateWorkflow(
			p.MainWorkflowByRuntime(Python, mainProgramWorkflow),
			p.PythonPolicyWorkflow(dirs)),
	}
}

// RunWorkflows fires workflow gorutines
func (p *PolicyTestCase) RunWorkflows(ctx context.Context) {
	for _, f := range p.workflows {
		go func(f_ func(ctx context.Context)) {
			f_(ctx)
		}(f)
	}
}

// RunScenarios executes the configured test scenarios for a given policy packs.
func (p *PolicyTestCase) RunScenarios(context.Context) {
	if p.numOfPolicyRuntimes.Load() == 0 {
		p.t.Fatalf("no policies were detected but listening loop was started")
	}
	go func() {
		for scenarioPath := range p.policyRuntimesQueue {
			p.RunScenario(scenarioPath)

			if p.numOfPolicyRuntimes.Add(-1) == 0 {
				break
			}
		}
		close(p.policyRuntimesDone)
	}()
}

// CloneEnvWithPath creates a clone of the Pulumi test environment, but with a different current working directory.
func CloneEnvWithPath(e *ptesting.Environment, cwd string) *ptesting.Environment {
	return &ptesting.Environment{
		T:                   e.T,
		RootPath:            e.RootPath,
		CWD:                 cwd,
		Backend:             e.Backend,
		Env:                 e.Env,
		Passphrase:          e.Passphrase,
		NoPassphrase:        e.NoPassphrase,
		UseLocalPulumiBuild: e.UseLocalPulumiBuild,
		Stdin:               e.Stdin,
	}
}

// CloneEnv creates a clone of the current Pulumi environment, keeping the same working directory.
func CloneEnv(e *ptesting.Environment) *ptesting.Environment {
	return CloneEnvWithPath(e, e.CWD)
}

// CreateStack initializes a new Pulumi stack and logs into the local Pulumi backend.
func (p *PolicyTestCase) CreateStack(context.Context) {
	e := CloneEnvWithPath(p.e, p.programDir)
	// Create the stack.
	e.RunCommand("pulumi", "login", "--local")
	abortIfFailed(p.t)

	e.RunCommand("pulumi", "stack", "init", p.stackName)
	abortIfFailed(p.t)
}

// DestroyStack cleans up and removes the Pulumi stack after the test is completed.
func (p *PolicyTestCase) DestroyStack() {
	e := CloneEnvWithPath(p.e, p.programDir)
	p.t.Log("Cleaning up Stack")
	e.RunCommand("pulumi", "destroy", "--yes")
	e.RunCommand("pulumi", "stack", "rm", "--yes")
}

// InstallPythonVenvOnce ensures that the Python virtual environment is set up only once, even if called multiple times.
func (p *PolicyTestCase) InstallPythonVenvOnce() {
	p.pythonVenv.Do(func() {
		p.e.RunCommand("pipenv", "--python", "3")
		abortIfFailed(p.t)
	})
}

// InstallProgram installs the required dependencies for the Pulumi program based on the runtime (NodeJS, Python, etc.).
func (p *PolicyTestCase) InstallProgram(context.Context) {
	e := CloneEnvWithPath(p.e, p.programDir)
	switch p.runtime {
	case NodeJS:
		e.RunCommand("yarn", "install")
		abortIfFailed(p.t)

	case Python:
		p.InstallPythonVenvOnce()
		e.RunCommand("pipenv", "run", "pip", "install", "-r", "requirements.txt")
		abortIfFailed(p.t)

	default:
		p.t.Fatalf("Unexpected runtime value.")
	}

	// Initial configuration.
	for k, v := range p.initialConfig {
		e.RunCommand("pulumi", "config", "set", k, v)
	}

}

// PythonPolicyWorkflow handles the installation of the Python policy pack as a dependency, if present.
func (p *PolicyTestCase) PythonPolicyWorkflow(dirs []string) func(ctx context.Context) {
	if Contains(dirs, "policy-pack-python") && p.IsPolicyRuntimePicked(Python) {
		p.numOfPolicyRuntimes.Add(1)
		p.hasPythonPack = true
		return p.InstallDependency(p.InstallPythonDep, filepath.Join(p.e.RootPath, "policy-pack-python"))
	}
	return func(ctx context.Context) {}
}

// NodeJSPolicyWorkflow handles the installation of the NodeJS policy pack as a dependency, if present.
func (p *PolicyTestCase) NodeJSPolicyWorkflow(dirs []string) func(ctx context.Context) {
	if Contains(dirs, "policy-pack") && p.IsPolicyRuntimePicked(NodeJS) {
		p.numOfPolicyRuntimes.Add(1)
		return p.InstallDependency(p.InstallNodeJSDep, filepath.Join(p.e.RootPath, "policy-pack"))
	}
	return func(ctx context.Context) {}
}

// TestComponentInstallation sets up the test component provider if the "testcomponent" directory is found.
func (p *PolicyTestCase) TestComponentInstallation(dirs []string) func(ctx context.Context) {
	if Contains(dirs, "testcomponent") {
		return func(ctx context.Context) {
			testComponentDir := filepath.Join(p.e.RootPath, "testcomponent")

			e := CloneEnvWithPath(p.e, testComponentDir)

			// Installs dependencies.
			e.RunCommand("go", "mod", "tidy")
			abortIfFailed(p.t)

			// Set the PATH envvar to the path to the testcomponent so the provider is available
			// to the program.
			p.e.Env = []string{pathEnvWith(testComponentDir)}
		}

	}
	return func(ctx context.Context) {}
}

// InstallPythonDep installs Python dependencies for the policy pack.
func (p *PolicyTestCase) InstallPythonDep(context.Context) {
	e := CloneEnv(p.e)

	p.InstallPythonVenvOnce()
	pythonPackDir := filepath.Join(e.RootPath, "policy-pack-python")
	pythonPackRequirements := filepath.Join(pythonPackDir, "requirements.txt")
	if _, err := os.Stat(pythonPackRequirements); !os.IsNotExist(err) {
		e.RunCommand("pipenv", "run", "pip", "install", "-r", pythonPackRequirements)
		abortIfFailed(p.t)
	}

	dep := filepath.Join("..", "..", "sdk", "python", "env", "src")
	dep, err := filepath.Abs(dep)
	assert.NoError(p.t, err)
	e.RunCommand("pipenv", "run", "pip", "install", "-e", dep)
	abortIfFailed(p.t)
}

// InstallNodeJSDep installs NodeJS dependencies for the policy pack.
func (p *PolicyTestCase) InstallNodeJSDep(context.Context) {

	e := CloneEnv(p.e)
	// Change to the Policy Pack directory.
	packDir := filepath.Join(e.RootPath, "policy-pack")
	e.CWD = packDir

	// Links @pulumi/policy.
	e.RunCommand("yarn", "link", "@pulumi/policy")
	abortIfFailed(p.t)

	// Get dependencies.
	e.RunCommand("yarn", "install")
	abortIfFailed(p.t)
}

// CheckPolicyPackConfig marshals the policy configuration to JSON, writes it to a file,
// and returns the corresponding command-line arguments to pass the config to Pulumi.
func (p *PolicyTestCase) CheckPolicyPackConfig(scenario policyTestScenario) (args []string) {

	if len(scenario.PolicyPackConfig) > 0 {
		// Marshal the config to JSON, with indentation for easier debugging.
		bytes, err := json.MarshalIndent(scenario.PolicyPackConfig, "", "    ")
		if err != nil {
			p.t.Fatalf("error marshalling policy config to JSON: %v", err)
		}

		// Change to the config directory.
		configDir := filepath.Join(p.e.RootPath, "config", scenario.Name)
		e := CloneEnvWithPath(p.e, configDir)

		// Write the JSON to a file.
		filename := "policy-config.json"
		e.WriteTestFile(filename, string(bytes))
		abortIfFailed(p.t)

		// Add the policy config argument.
		policyConfigFile := filepath.Join(configDir, filename)
		args = append(args, "--policy-pack-config", policyConfigFile)
	}
	return
}

// RunScenario executes a single policy test scenario and checks for the expected errors.
func (p *PolicyTestCase) RunScenario(policyPackDirectoryPath string) {
	e := CloneEnvWithPath(p.e, p.programDir)
	p.t.Run(policyPackDirectoryPath, func(t *testing.T) {
		e.T = t

		// Clean up the stack after running through the scenarios, so that subsequent runs
		// begin on a clean slate.
		defer func() {
			e.RunCommand("pulumi", "destroy", "--yes")
			abortIfFailed(t)
		}()

		for idx, scenario := range p.scenarios {
			// Create a sub-test so go test will output data incrementally, which will let
			// a CI system like Travis know not to kill the job if no output is sent after 10m.
			// idx+1 to make it 1-indexed.
			scenario.Name = fmt.Sprintf("scenario_%d", idx+1)
			t.Run(scenario.Name, func(t *testing.T) {
				e.T = t

				e.RunCommand("pulumi", "config", "set", "scenario", fmt.Sprintf("%d", idx+1))

				cmd, args := getCmdArgs(p.runtime == Python || p.hasPythonPack, policyPackDirectoryPath)

				// If there is config for the scenario, write it out to a file and pass the file path
				// as a --policy-pack-config argument.
				args = append(args, p.CheckPolicyPackConfig(scenario)...)

				if len(scenario.WantErrors) == 0 {
					t.Log("No errors are expected.")
					e.RunCommand(cmd, args...)
				} else {
					var stdout, stderr string
					if scenario.Advisory {
						stdout, stderr = e.RunCommand(cmd, args...)
					} else {
						stdout, stderr = e.RunCommandExpectError(cmd, args...)
					}

					for _, wantErr := range scenario.WantErrors {
						inSTDOUT := strings.Contains(stdout, wantErr)
						inSTDERR := strings.Contains(stderr, wantErr)

						if !inSTDOUT && !inSTDERR {
							t.Errorf("Did not find expected error %q", wantErr)
						}
					}

					if t.Failed() {
						t.Logf("Command output:\nSTDOUT:\n%v\n\nSTDERR:\n%v\n\n", stdout, stderr)
					}
				}
			})
		}
	})
}

// CancelIfTestFailed monitors the test's status and cancels the context if the test fails.
func (p *PolicyTestCase) CancelIfTestFailed(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if p.t.Failed() || ctx.Err() != nil {
				cancel()
				return
			}
			time.Sleep(time.Millisecond * 100)
		}
	}()
	return ctx
}

func (p *PolicyTestCase) Run() {
	p.t.Logf("Running Policy Pack Integration Test from directory %q", p.testDirName)

	assert.True(p.t, len(p.scenarios) > 0, "no test scenarios provided")

	ctx := p.CancelIfTestFailed(context.Background())

	// Get the directory for the policy pack to run.
	cwd, err := os.Getwd()
	if err != nil {
		p.t.Fatalf("Error getting working directory")
	}
	rootDir := filepath.Join(cwd, p.testDirName)

	// The Pulumi project name matches the test dir name in these tests.
	os.Setenv("PULUMI_TEST_PROJECT", p.testDirName)

	p.stackName = fmt.Sprintf("%s-%d", p.testDirName, time.Now().Unix()%100000)
	os.Setenv("PULUMI_TEST_STACK", p.stackName)

	// Copy the root directory to /tmp and run various operations within that directory.

	p.e = ptesting.NewEnvironment(p.t)
	defer func() {
		if !p.t.Failed() {
			p.e.DeleteEnvironment()
		}
	}()
	p.e.ImportDirectory(rootDir)
	p.programDir = filepath.Join(p.e.RootPath, "program")

	p.ConstructTestWorkflow()
	assert.False(p.t, p.numOfPolicyRuntimes.Load() == 0, "no policy runtimes were discovered")
	if p.numOfPolicyRuntimes.Load() == 0 {
		return
	}

	p.RunWorkflows(context.TODO())

	defer p.DestroyStack()

	// Wait until all workflows finish.
	select {
	case <-p.policyRuntimesDone:
	case <-ctx.Done():
	}

	p.t.Log("Finished test scenarios.")
	// Cleanup already registered via defer.
}

// runPolicyPackIntegrationTest runs a Pulumi policy pack integration test in parallel, unless specific synchronization is required.
func runPolicyPackIntegrationTest(
	t *testing.T, testDirName string, runtime Runtime,
	initialConfig map[string]string, scenarios []policyTestScenario) {

	// TODO it's actually hack
	// TODO some test case require environment management which is not possible for parallel execution
	syncRun := false
	if initialConfig != nil {
		if _, exist := initialConfig["SYNC"]; exist {
			syncRun = true
		}
	}
	if !syncRun {
		t.Parallel()
	}

	NewCase(t, testDirName, runtime, initialConfig, scenarios)
}

// Test invalid policies.
func TestInvalidPolicy(t *testing.T) {
	runPolicyPackIntegrationTest(t, "invalid_policy", NodeJS, nil, []policyTestScenario{
		{
			WantErrors: []string{`Invalid policy name "all". "all" is a reserved name.`},
		},
		{
			WantErrors: []string{`enforcementLevel cannot be explicitly specified in properties.`},
		},
		{
			WantErrors: []string{`"enforcementLevel" cannot be specified in required.`},
		},
	})
}

// Test basic resource validation.
func TestValidateResource(t *testing.T) {
	runPolicyPackIntegrationTest(t, "validate_resource", NodeJS, nil, []policyTestScenario{
		// Test scenario 1: no resources.
		{
			WantErrors: nil,
		},
		// Test scenario 2: no violations.
		{
			WantErrors: nil,
		},
		// Test scenario 3: violates the first policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-1  (pulumi-nodejs:dynamic:Resource: a)",
				"Prohibits setting state to 1 on dynamic resources.",
				"'state' must not have the value 1.",
			},
		},
		// Test scenario 4: violates the second policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-2  (pulumi-nodejs:dynamic:Resource: b)",
				"Prohibits setting state to 2 on dynamic resources.",
				"'state' must not have the value 2.",
			},
		},
		// Test scenario 5: violates the first validation function of the third policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-3-or-4  (pulumi-nodejs:dynamic:Resource: c)",
				"Prohibits setting state to 3 or 4 on dynamic resources.",
				"'state' must not have the value 3.",
			},
		},
		// Test scenario 6: violates the second validation function of the third policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-3-or-4  (pulumi-nodejs:dynamic:Resource: d)",
				"Prohibits setting state to 3 or 4 on dynamic resources.",
				"'state' must not have the value 4.",
			},
		},
		// Test scenario 7: violates the fourth policy.
		{
			WantErrors: []string{
				"[mandatory]  randomuuid-no-keepers  (random:index/randomUuid:RandomUuid: r1)",
				"Prohibits creating a RandomUuid without any 'keepers'.",
				"RandomUuid must not have an empty 'keepers'.",
			},
		},
		// Test scenario 8: no violations.
		{
			WantErrors: nil,
		},
		// Test scenario 9: violates the fifth policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-5  (pulumi-nodejs:dynamic:Resource: e)",
				"Prohibits setting state to 5 on dynamic resources.",
				"'state' must not have the value 5.",
			},
		},
		// Test scenario 10: no violations.
		{
			WantErrors: nil,
		},
		// Test scenario 11: no violations.
		// Test the ability to send large gRPC messages (>4mb).
		// Issue: https://github.com/pulumi/pulumi/issues/4155
		{
			WantErrors: nil,
		},
	})
}

// Test basic resource validation of a Python program.
func TestValidatePythonResource(t *testing.T) {
	runPolicyPackIntegrationTest(t, "validate_python_resource", Python, nil, []policyTestScenario{
		// Test scenario 1: violates the policy.
		{
			WantErrors: []string{
				"[mandatory]  randomuuid-no-keepers  (random:index/randomUuid:RandomUuid: r1)",
				"Prohibits creating a RandomUuid without any 'keepers'.",
				"RandomUuid must not have an empty 'keepers'.",
			},
		},
		// Test scenario 2: violates the policy.
		{
			WantErrors: []string{
				"[mandatory]  randomuuid-no-keepers  (random:index/randomUuid:RandomUuid: r2)",
				"Prohibits creating a RandomUuid without any 'keepers'.",
				"RandomUuid must not have an empty 'keepers'.",
			},
		},
		// Test scenario 3: no violations.
		{
			WantErrors: nil,
		},
	})
}

// Test basic stack validation.
func TestValidateStack(t *testing.T) {
	runPolicyPackIntegrationTest(t, "validate_stack", NodeJS, nil, []policyTestScenario{
		// Test scenario 1: no resources.
		{
			WantErrors: nil,
		},
		// Test scenario 2: no violations.
		{
			WantErrors: nil,
		},
		// Test scenario 3: violates the first policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-1",
				"Prohibits setting state to 1 on dynamic resources.",
				"'state' must not have the value 1.",
			},
		},
		// Test scenario 4: violates the second policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-2",
				"Prohibits setting state to 2 on dynamic resources.",
				"'state' must not have the value 2.",
			},
		},
		// Test scenario 5: violates the third policy.
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-state-with-value-3  (pulumi-nodejs:dynamic:Resource: c)",
				"Prohibits setting state to 3 on dynamic resources.",
				"'state' must not have the value 3.",
			},
		},
		// Test scenario 6: violates the fourth policy.
		{
			WantErrors: []string{
				"[mandatory]  randomuuid-no-keepers",
				"Prohibits creating a RandomUuid without any 'keepers'.",
				"RandomUuid must not have an empty 'keepers'.",
			},
		},
		// Test scenario 7: violates the fifth policy.
		{
			WantErrors: []string{
				"[mandatory]  no-randomstrings",
				"Prohibits RandomString resources.",
				"RandomString resources are not allowed.",
			},
		},
		// Test scenario 8: no violations.
		{
			WantErrors: nil,
		},
		// Test scenario 9: no violations.
		{
			WantErrors: nil,
		},
		// Test scenario 10: a stack validation with enforcement level of "remediate" is treated as "mandatory".
		{
			WantErrors: []string{
				"[mandatory]  dynamic-no-foo-with-value-bar",
				"Prohibits setting foo to 'bar' on dynamic resources.",
				"'foo' must not have the value 'bar'.",
			},
		},
	})
}

// Test that accessing unknown values returns an error during previews.
func TestUnknownValues(t *testing.T) {
	runPolicyPackIntegrationTest(t, "unknown_values", NodeJS, map[string]string{
		"aws:region": "us-west-2",
	}, []policyTestScenario{
		{
			WantErrors: []string{
				"unknown-values-policy@v0.0.1",
				"[advisory]  unknown-values-resource-validation  (random:index/randomPet:RandomPet: pet)",
				"can't run policy 'unknown-values-resource-validation' from policy pack 'unknown-values-policy@v0.0.1' during preview: string value at .prefix can't be known during preview",
				"[advisory]  unknown-values-stack-validation",
				"can't run policy 'unknown-values-stack-validation' from policy pack 'unknown-values-policy@v0.0.1' during preview: string value at .prefix can't be known during preview",
			},
			Advisory: true,
		},
	})
}

// Test runtime data (Config, getStack, getProject, and isDryRun) is available to the Policy Pack.
func TestRuntimeData(t *testing.T) {
	runPolicyPackIntegrationTest(t, "runtime_data", NodeJS, map[string]string{
		"aConfigValue": "this value is a value",
		"aws:region":   "us-west-2",

		"SYNC": "SYNC", // TODO it's actually hack
		// TODO this test case require environment management which is not possible for parallel execution
	}, []policyTestScenario{{WantErrors: nil}})
}

// Test resource options.
func TestResourceOptions(t *testing.T) {
	runPolicyPackIntegrationTest(t, "resource_options", NodeJS, nil, []policyTestScenario{
		// Test scenario 1: test resource options.
		{WantErrors: nil},
		// Test scenario 2: prepare for destroying the stack (unprotect protected resources).
		{WantErrors: nil},
	})
}

// Test parent and dependencies.
func TestParentDependencies(t *testing.T) {
	runPolicyPackIntegrationTest(t, "parent_dependencies", NodeJS, nil, []policyTestScenario{
		{WantErrors: nil},
	})
}

// Test provider.
func TestProvider(t *testing.T) {
	runPolicyPackIntegrationTest(t, "provider", NodeJS, nil, []policyTestScenario{
		{WantErrors: nil},
	})
}

// Test Policy Packs with enforcement levels set on the Policy Pack and individual policies.
func TestEnforcementLevel(t *testing.T) {
	runPolicyPackIntegrationTest(t, "enforcementlevel", NodeJS, nil, []policyTestScenario{
		// Test scenario 1: Policy Pack: advisory; Policy: advisory.
		{
			WantErrors: []string{
				"[advisory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[advisory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
			Advisory: true,
		},
		// Test scenario 2: Policy Pack: advisory; Policy: disabled.
		{
			WantErrors: nil,
		},
		// Test scenario 3: Policy Pack: advisory; Policy: mandatory.
		{
			WantErrors: []string{
				"[mandatory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[mandatory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
		},
		// Test scenario 4: Policy Pack: advisory; Policy: not set.
		{
			WantErrors: []string{
				"[advisory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[advisory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
			Advisory: true,
		},
		// Test scenario 5: Policy Pack: disabled; Policy: advisory.
		{
			WantErrors: []string{
				"[advisory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[advisory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
			Advisory: true,
		},
		// Test scenario 6: Policy Pack: disabled; Policy: disabled.
		{
			WantErrors: nil,
		},
		// Test scenario 7: Policy Pack: disabled; Policy: mandatory.
		{
			WantErrors: []string{
				"[mandatory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[mandatory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
		},
		// Test scenario 8: Policy Pack: disabled; Policy: not set.
		{
			WantErrors: nil,
		},
		// Test scenario 9: Policy Pack: mandatory; Policy: advisory.
		{
			WantErrors: []string{
				"[advisory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[advisory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
			Advisory: true,
		},
		// Test scenario 10: Policy Pack: mandatory; Policy: disabled.
		{
			WantErrors: nil,
		},
		// Test scenario 11: Policy Pack: mandatory; Policy: mandatory.
		{
			WantErrors: []string{
				"[mandatory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[mandatory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
		},
		// Test scenario 12: Policy Pack: mandatory; Policy: not set.
		{
			WantErrors: []string{
				"[mandatory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[mandatory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
		},
		// Test scenario 13: Policy Pack: not set; Policy: advisory.
		{
			WantErrors: []string{
				"[advisory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[advisory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
			Advisory: true,
		},
		// Test scenario 14: Policy Pack: not set; Policy: disabled.
		{
			WantErrors: nil,
		},
		// Test scenario 15: Policy Pack: not set; Policy: mandatory.
		{
			WantErrors: []string{
				"[mandatory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[mandatory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
		},
		// Test scenario 16: Policy Pack: not set; Policy: not set.
		{
			WantErrors: []string{
				"[advisory]  validate-resource  (random:index/randomString:RandomString: str)",
				"Always reports a resource violation.",
				"validate-resource-violation-message",
				"[advisory]  validate-stack",
				"Always reports a stack violation.",
				"validate-stack-violation-message",
			},
			Advisory: true,
		},
	})
}

// Test Policy Pack configuration.
func TestConfig(t *testing.T) {
	const (
		resourcePolicy = "resource-validation"
		stackPolicy    = "stack-validation"
		errorPreamble  = "error: validating policy config: config-policy 0.0.1  "
	)

	config := func(c PolicyConfig) map[string]PolicyConfig {
		return map[string]PolicyConfig{
			resourcePolicy: c,
			stackPolicy:    c,
		}
	}

	want := func(err ...string) []string {
		var result []string
		for _, e := range err {
			result = append(result,
				errorPreamble+resourcePolicy+": "+e,
				errorPreamble+stackPolicy+": "+e,
			)
		}
		return result
	}

	runPolicyPackIntegrationTest(t, "config", NodeJS, nil, []policyTestScenario{
		// Test senario 1: String from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": "bar",
			}),
			WantErrors: nil,
		},
		// Test scenario 2: Default string value specified in schema used.
		{
			WantErrors: nil,
		},
		// Test scenario 3: Default number value specified in schema used.
		{
			WantErrors: nil,
		},
		// Test scenario 4: Specified config value overrides default value.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": "overridden",
			}),
			WantErrors: nil,
		},
		// Test scenario 5: Default value specified in schema for required field used.
		{
			WantErrors: nil,
		},
		// Test scenario 6: Required config property not set.
		{
			WantErrors: want("foo is required"),
		},
		// Test scenario 7: Default value set to incorrect type.
		{
			WantErrors: want("foo: Invalid type. Expected: string, given: integer"),
		},
		// Test scenario 8: Default value too long.
		{
			WantErrors: want("foo: String length must be less than or equal to 3"),
		},
		// Test scenario 9: Default value too short.
		{
			WantErrors: want("foo: String length must be greater than or equal to 50"),
		},
		// Test scenario 10: Default value set to invalid enum value.
		{
			WantErrors: want(`foo: foo must be one of the following: "bar", "baz"`),
		},
		// Test scenario 11: Default value set to invalid constant value.
		{
			WantErrors: want(`foo: foo does not match: "bar"`),
		},
		// Test scenario 12: Incorrect type.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": 1,
			}),
			WantErrors: want(`foo: Invalid type. Expected: string, given: integer`),
		},
		// Test scenario 13: Invalid enum value.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": "blah",
			}),
			WantErrors: want(`foo: foo must be one of the following: "bar", "baz"`),
		},
		// Test scenario 14: Invalid constant value.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": "blah",
			}),
			WantErrors: want(`foo: foo does not match: "bar"`),
		},
		// Test scenario 15: Multiple validation errors.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": "this is too long",
				"bar": float64(3.14),
			}),
			WantErrors: want(
				`bar: Invalid type. Expected: integer, given: number`,
				`foo: String length must be less than or equal to 3`,
			),
		},
		// Test scenario 16: Number (int) from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": 42,
			}),
			WantErrors: nil,
		},
		// Test scenario 17: Number (float) from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": float64(3.14),
			}),
			WantErrors: nil,
		},
		// Test scenario 18: Integer from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": 42,
			}),
			WantErrors: nil,
		},
		// Test scenario 19: Boolean (true) from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": true,
			}),
			WantErrors: nil,
		},
		// Test scenario 20: Boolean (false) from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": false,
			}),
			WantErrors: nil,
		},
		// Test scenario 21: Object from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": map[string]interface{}{"bar": "baz"},
			}),
			WantErrors: nil,
		},
		// Test scenario 22: Array from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": []string{"a", "b", "c"},
			}),
			WantErrors: nil,
		},
		// Test scenario 23: Null from config.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": nil,
			}),
			WantErrors: nil,
		},
		// Test scenario 24: Initial config.
		{
			WantErrors: nil,
		},
		// Test scenario 25: Initial config overridden.
		{
			PolicyPackConfig: config(PolicyConfig{
				"foo": "overridden",
			}),
			WantErrors: nil,
		},
	})
}

// Test deserializing resource properties.
func TestDeserialize(t *testing.T) {
	runPolicyPackIntegrationTest(t, "deserialize", NodeJS, nil, []policyTestScenario{
		{WantErrors: nil},
	})
}

// Test using a remote component with a nested unknown, ensuring the unknown remains at the leaf position and does
// not end up making the whole top-level property unknown.
func TestRemoteComponent(t *testing.T) {
	runPolicyPackIntegrationTest(t, "remote_component", NodeJS, nil, []policyTestScenario{
		{
			WantErrors: []string{
				"remote-component-policy@v0.0.1",
				"[advisory]  resource-validation  (random:index/randomString:RandomString: innerRandom)",
				"can't run policy 'resource-validation' from policy pack 'remote-component-policy@v0.0.1' during preview: string value at .keepers.hi can't be known during preview",
			},
			Advisory: true,
		},
	})
}

func TestPythonDoesNotTryToInstallPlugin(t *testing.T) {
	e := ptesting.NewEnvironment(t)
	t.Log(e.RootPath)
	defer e.DeleteIfNotFailed()

	pulumiHome := t.TempDir()
	t.Setenv("PULUMI_HOME", pulumiHome)

	e.RunCommand("pulumi", "login", "--local")
	e.RunCommand("pulumi", "new", "python", "--generate-only", "--yes", "--force")

	requirementsTxtPath := filepath.Join(e.RootPath, "requirements.txt")

	dep, err := filepath.Abs(filepath.Join("..", "..", "sdk", "python", "lib"))
	require.NoError(t, err)

	file, err := os.OpenFile(requirementsTxtPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer file.Close()
	_, err = file.WriteString(dep)
	require.NoError(t, err)

	// Pulumi should not try to install a plugin for pulumi_policy.
	// This command will fail if it tries to install the non-existent pulumi_policy plugin.
	e.RunCommand("pulumi", "install")
}
