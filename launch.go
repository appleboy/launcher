package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/screwdriver-cd/launcher/executor"
	// "github.com/screwdriver-cd/launcher/git"
	"github.com/screwdriver-cd/launcher/screwdriver"
	"github.com/urfave/cli"
)

// VERSION gets set by the build script via the LDFLAGS
var VERSION string

var mkdirAll = os.MkdirAll
var stat = os.Stat
// var newRepo = git.New
var open = os.Open
var executorRun = executor.Run
var writeFile = ioutil.WriteFile
var newEmitter = screwdriver.NewEmitter
var execCommand = exec.Command

var cleanExit = func() {
	os.Exit(0)
}

// exit sets the build status and exits successfully
func exit(status screwdriver.BuildStatus, buildID string, api screwdriver.API) {
	if api != nil {
		log.Printf("Setting build status to %s", status)
		if err := api.UpdateBuildStatus(status, buildID); err != nil {
			log.Printf("Failed updating the build status: %v", err)
		}
	}

	cleanExit()
}

// type repo.Path struct {
// 	Host   string
// 	Org    string
// 	Repo   string
// 	Branch string
// }

// func (s scmPath) httpsString() string {
// 	return fmt.Sprintf("https://%s/%s/%s#%s", s.Host, s.Org, s.Repo, s.Branch)
// }

// e.g. scmUri: "github:123456:master", scmName: "screwdriver-cd/launcher"
func parseScmUri(scmUri, scmName string) (scmPath, error) {
	uri := strings.Split(scmUri, ":")
	orgRepo := strings.Split(scmName, "/")

	if len(uri) != 3 || len(orgRepo) != 2 {
		return scmPath{}, fmt.Errorf("unable to parse scmUri %v and scmName %v", scmUri, scmName)
	}

	return scmPath{
		Host:   uri[0],
		Org:    orgRepo[0],
		Repo:   orgRepo[1],
		Branch: uri[2],
	}, nil
}

// A Workspace is a description of the paths available to a Screwdriver build
type Workspace struct {
	Root      string
	Src       string
	Artifacts string
}

// createWorkspace makes a Scrwedriver workspace from path components
// e.g. ["github.com", "screwdriver-cd"] creates
//     /sd/workspace/src/github.com/screwdriver-cd
//     /sd/workspace/artifacts
func createWorkspace(rootDir string, srcPaths ...string) (Workspace, error) {
	srcPaths = append([]string{"src"}, srcPaths...)
	src := path.Join(srcPaths...)

	src = path.Join(rootDir, src)
	artifacts := path.Join(rootDir, "artifacts")

	paths := []string{
		src,
		artifacts,
	}
	for _, p := range paths {
		_, err := stat(p)
		if err == nil {
			msg := "Cannot create workspace path %q, path already exists."
			return Workspace{}, fmt.Errorf(msg, p)
		}
		err = mkdirAll(p, 0777)
		if err != nil {
			return Workspace{}, fmt.Errorf("Cannot create workspace path %q: %v", p, err)
		}
	}

	w := Workspace{
		Root:      rootDir,
		Src:       src,
		Artifacts: artifacts,
	}
	return w, nil
}

func writeArtifact(aDir string, fName string, artifact interface{}) error {
	data, err := json.MarshalIndent(artifact, "", strings.Repeat(" ", 4))
	if err != nil {
		return fmt.Errorf("marshaling artifact: %v ", err)
	}

	pathToCreate := path.Join(aDir, fName)
	err = writeFile(pathToCreate, data, 0644)
	if err != nil {
		return fmt.Errorf("creating file %q : %v", pathToCreate, err)
	}

	return nil
}
//
// // prNumber checks to see if the job name is a pull request and returns its number
// func prNumber(jobName string) string {
// 	r := regexp.MustCompile("^PR-([0-9]+)$")
// 	matched := r.FindStringSubmatch(jobName)
// 	if matched == nil || len(matched) != 2 {
// 		return ""
// 	}
// 	log.Println("Build is a PR: ", matched[1])
// 	return matched[1]
// }

func launch(api screwdriver.API, buildID, rootDir, emitterPath string) error {
	emitter, err := newEmitter(emitterPath)
	if err != nil {
		return err
	}
	defer emitter.Close()

	if err = api.UpdateStepStart(buildID, "sd-setup"); err != nil {
		return fmt.Errorf("updating sd-setup start: %v", err)
	}

	log.Print("Setting Build Status to RUNNING")
	if err = api.UpdateBuildStatus(screwdriver.Running, buildID); err != nil {
		return fmt.Errorf("updating build status to RUNNING: %v", err)
	}

	log.Printf("Fetching Build %v", buildID)
	b, err := api.BuildFromID(buildID)
	if err != nil {
		return fmt.Errorf("fetching build ID %q: %v", buildID, err)
	}

	log.Printf("Fetching Job %v", b.JobID)
	j, err := api.JobFromID(b.JobID)
	if err != nil {
		return fmt.Errorf("fetching Job ID %q: %v", b.JobID, err)
	}

	log.Printf("Fetching Pipeline %v", j.PipelineID)
	p, err := api.PipelineFromID(j.PipelineID)
	if err != nil {
		return fmt.Errorf("fetching Pipeline ID %q: %v", j.PipelineID, err)
	}

	scm, err := parseScmUri(p.ScmUri, p.ScmRepo.Name)
	if err != nil {
		return err
	}

	log.Printf("Creating Workspace in %v", rootDir)
	w, err := createWorkspace(rootDir, scm.Host, scm.Org)
	if err != nil {
		return err
	}

	oldJobName := j.Name

	// Get checkout commands from build
	execCommand("cd", w.src).Run()
	checkoutCommand := b.steps[1].command



	// pr := prNumber(j.Name)
	// if pr != "" {
	// 	j.Name = "main"
	// }
	//
	// // For PRs, we are fine with merging to the latest version of the branch.
	// // The SHA that we get from the Build is the SHA of the commit that we are building.
	// checkoutSHA := b.SHA
	// if pr != "" {
	// 	checkoutSHA = scm.Branch
	// }
	//
	// repo, err := newRepo(scm.httpsString(), checkoutSHA, w.Src, emitter)
	// if err != nil {
	// 	return err
	// }
	//
	// err = repo.Checkout()
	// if err != nil {
	// 	return err
	// }
	//
	// if pr != "" {
	// 	err = repo.MergePR(pr, b.SHA)
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	err = writeArtifact(w.Artifacts, "steps.json", b.Commands)
	if err != nil {
		return fmt.Errorf("creating steps.json artifact: %v", err)
	}

	err = writeArtifact(w.Artifacts, "environment.json", b.Environment)
	if err != nil {
		return fmt.Errorf("creating environment.json artifact: %v", err)
	}

	defaultEnv := map[string]string{
		"SCREWDRIVER": "true",
		"CI":          "true",
		"CONTINUOUS_INTEGRATION": "true",
		"SD_JOB_NAME":            oldJobName,
		"SD_PULL_REQUEST":        pr,
		"SD_SOURCE_DIR":          repo.Path(),
		"SD_ARTIFACTS_DIR":       w.Artifacts,
	}

	secrets, err := api.SecretsForBuild(b)
	if err != nil {
		return fmt.Errorf("Fetching secrets for build %s", b.ID)
	}

	env := createEnvironment(defaultEnv, secrets)

	if err := api.UpdateStepStop(buildID, "sd-setup", 0); err != nil {
		return fmt.Errorf("updating sd-setup stop: %v", err)
	}

	if err := executorRun(repo.Path(), env, emitter, b, api, buildID); err != nil {
		return err
	}

	return nil
}

func createEnvironment(base map[string]string, secrets screwdriver.Secrets) []string {
	combined := map[string]string{}

	// Start with the current environment
	for _, e := range os.Environ() {
		pieces := strings.SplitAfterN(e, "=", 2)
		if len(pieces) != 2 {
			log.Printf("WARN: bad environment value from base environment: %s", e)
			continue
		}

		k := pieces[0][:len(pieces[0])-1] // Drop the "=" off the end
		v := pieces[1]

		combined[k] = v
	}

	// Add the base environment values
	for k, v := range base {
		combined[k] = v
	}

	// Add secrets to the environment
	for _, s := range secrets {
		combined[s.Name] = s.Value
	}

	// Delete any environment variables that we don't want the user to accidentally dump
	for _, k := range []string{
		"SD_TOKEN",
	} {
		delete(combined, k)
	}

	// Create the final string slice
	envStrings := []string{}
	for k, v := range combined {
		envStrings = append(envStrings, strings.Join([]string{k, v}, "="))
	}
	return envStrings
}

// Executes the command based on arguments from the CLI
func launchAction(api screwdriver.API, buildID, rootDir, emitterPath string) error {
	log.Printf("Starting Build %v\n", buildID)

	if err := launch(api, buildID, rootDir, emitterPath); err != nil {
		if _, ok := err.(executor.ErrStatus); ok {
			log.Printf("Failure due to non-zero exit code: %v\n", err)
		} else {
			log.Printf("Error running launcher: %v\n", err)
		}

		exit(screwdriver.Failure, buildID, api)
		return nil
	}

	exit(screwdriver.Success, buildID, api)
	return nil
}

func recoverPanic(buildID string, api screwdriver.API) {
	if p := recover(); p != nil {
		filename := fmt.Sprintf("launcher-stacktrace-%s", time.Now().Format(time.RFC3339))
		tracefile := filepath.Join(os.TempDir(), filename)

		log.Printf("ERROR: Internal Screwdriver error. Please file a bug about this: %v", p)
		log.Printf("ERROR: Writing StackTrace to %s", tracefile)
		err := ioutil.WriteFile(tracefile, debug.Stack(), 0600)
		if err != nil {
			log.Printf("ERROR: Unable to write stacktrace to file: %v", err)
		}

		exit(screwdriver.Failure, buildID, api)
	}
}

// finalRecover makes one last attempt to recover from a panic.
// This should only happen if the previous recovery caused a panic.
func finalRecover() {
	if p := recover(); p != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Something terrible has happened. Please file a ticket with this info:")
		fmt.Fprintf(os.Stderr, "ERROR: %v\n%v\n", p, debug.Stack())
	}
	cleanExit()
}

func main() {
	defer finalRecover()
	defer recoverPanic("", nil)

	app := cli.NewApp()
	app.Name = "launcher"
	app.Usage = "launch a Screwdriver build"
	app.UsageText = "launch [options] build-id"
	app.Copyright = "(c) 2016 Yahoo Inc."

	if VERSION == "" {
		VERSION = "0.0.0"
	}
	app.Version = VERSION

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "api-uri",
			Usage: "API URI for Screwdriver",
			Value: "http://localhost:8080",
		},
		cli.StringFlag{
			Name:   "token",
			Usage:  "JWT used for accessing Screwdriver's API",
			EnvVar: "SD_TOKEN",
		},
		cli.StringFlag{
			Name:  "workspace",
			Usage: "Location for checking out and running code",
			Value: "/sd/workspace",
		},
		cli.StringFlag{
			Name:  "emitter",
			Usage: "Location for writing log lines to",
			Value: "/var/run/sd/emitter",
		},
	}

	app.Action = func(c *cli.Context) error {
		url := c.String("api-uri")
		token := c.String("token")
		workspace := c.String("workspace")
		emitterPath := c.String("emitter")
		buildID := c.Args().Get(0)

		if buildID == "" {
			return cli.ShowAppHelp(c)
		}

		api, err := screwdriver.New(url, token)
		if err != nil {
			log.Printf("Error creating Screwdriver API %v: %v", buildID, err)
			exit(screwdriver.Failure, buildID, nil)
		}

		defer recoverPanic(buildID, api)

		launchAction(api, buildID, workspace, emitterPath)

		// This should never happen...
		log.Println("Unexpected return in launcher. Failing the build.")
		exit(screwdriver.Failure, buildID, api)
		return nil
	}
	app.Run(os.Args)
}
