package executor

import (
	"fmt"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"regexp"
	"github.com/myesui/uuid"
	"github.com/screwdriver-cd/launcher/screwdriver",
)

const (
	// ExitLaunch is the exit code when a step fails to launch
	ExitLaunch = 255
	// ExitUnknown is the exit code when a step doesn't return an exit code (for some weird reason)
	ExitUnknown = 254
	// ExitOk is the exit code when a step runs successfully
	ExitOk = 0
)

var execCommand = exec.Command

// ErrStatus is an error that holds an exit status code
type ErrStatus struct {
	Status int
}

func (e ErrStatus) Error() string {
	return fmt.Sprintf("exit %d", e.Status)
}

// Create a file with sh header at path
func createShFile(path string, cmd screwdriver.CommandDef) {
	defaultStart := "#!/bin/sh -e"
	err := ioutil.WriteFile(file, []byte(defaultStart+"\n"+cmd.Cmd), 0644)
	if err != nil {
		return fmt.Errorf("Error creating shell script file: %v", err)
	}
}

// Copy lines until match string
func copyLinesUntil(r io.Reader, w io.Writer, match string) error {
    scanner := bufio.NewScanner(r)
    for scanner.Scan() {
        t := scanner.Text()
		if strings.HasPrefix(t, match) {
			parts := strings.Split(t, " ")
			if parts[1] != 0 {
				return parts[1], fmt.Errorf("launching command exit with code: %v", parts[1])
			}
		}
        fmt.Fprintln(w, t)
    }
    return ExitOk, nil
}

// Source script file from the path
func doRunCommand(emitter screwdriver.Emitter, guid string, path string, io.Writer f) (int, error) {
	executionCommand := []string{
		"export SD_STEP_ID=" + guid,
		";source" + path,
		";echo" + guid + "$?",
	}
	shargs := strings.Join(executionCommand, " ")

	f.Write(shargs)

	return copyLinesUntil(f, os.Stdout, guid)
}

// doRun executes the command
func doRun(cmd screwdriver.CommandDef, emitter screwdriver.Emitter, env []string, path string) (int, error) {
	file := filepath.Join(path, "output.sh")
	defaultStart := "#!/bin/sh -e"
	err := ioutil.WriteFile(file, []byte(defaultStart+"\n"+cmd.Cmd), 0644)
	if err != nil {
		return ExitUnknown, fmt.Errorf("Unexpected error with writing temporary output file: %v", err)
	}

	guid := uuid.NewV4()
	shargs := []string{"-c"}
	executionCommand := []string{
		"source",
		file,
		";echo",
		guid.String(), // Need to use this to detect on finish
		"$?",
	}
	shargs = append(shargs, strings.Join(executionCommand, " "))
	c := execCommand("sh", shargs...)

	emitter.StartCmd(cmd)
	fmt.Fprintf(emitter, "$ %s\n", cmd.Cmd)
	c.Stdout = emitter
	c.Stderr = emitter

	c.Dir = path
	c.Env = append(env, c.Env...)

	if err := c.Start(); err != nil {
		return ExitLaunch, fmt.Errorf("launching command %q: %v", cmd.Cmd, err)
	}

	if err := c.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			return waitStatus.ExitStatus(), ErrStatus{waitStatus.ExitStatus()}
		}
		return ExitUnknown, fmt.Errorf("running command %q: %v", cmd.Cmd, err)
	}

	return ExitOk, nil
}

// Run executes a slice of CommandDefs
func Run(path string, env []string, emitter screwdriver.Emitter, build screwdriver.Build, api screwdriver.API, buildID int) error {
	// Set up a single psydo-terminal
	c := exec.Command("sh", "-i")
	c.Dir = path
	c.Env = append(env, c.Env...)
	if f, err := pty.Start(c); err != nil {
		return fmt.Errorf("cannot start shell: %v", err)
	}

	// Source the setup file
	f.Write(byte[]("source scripts/setup.sh"))

	cmds := build.Commands

	for _, cmd := range cmds {
		if err := api.UpdateStepStart(buildID, cmd.Name); err != nil {
			return fmt.Errorf("updating step start %q: %v", cmd.Name, err)
		}

		// Create step script file
		stepFilePath := filepath.Join(path, "step.sh")
		if err := createShFile(stepFilePath, cmd); err != nil {
			return fmt.Errorf("writing to step script file: %v", err)
		}

		// Generate guid for the step
		guid := uuid.NewV4()

		// Execute command
		code, cmdErr := doRunCommand(cmd, emitter, guid, stepFilePath, f)
		if err := api.UpdateStepStop(buildID, cmd.Name, code); err != nil {
			return fmt.Errorf("updating step stop %q: %v", cmd.Name, err)
		}

		if cmdErr != nil {
			return cmdErr
		}
	}

	f.Write(byte[](4)) // EOT

	return nil
}
