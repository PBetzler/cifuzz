package maven

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"code-intelligence.com/cifuzz/internal/build"
	"code-intelligence.com/cifuzz/internal/cmdutils"
	"code-intelligence.com/cifuzz/pkg/log"
	"code-intelligence.com/cifuzz/util/fileutil"
)

type ParallelOptions struct {
	Enabled bool
	NumJobs uint
}

type BuilderOptions struct {
	ProjectDir string
	Parallel   ParallelOptions
	Stdout     io.Writer
	Stderr     io.Writer
}

func (opts *BuilderOptions) Validate() error {
	// Check that the project dir is set
	if opts.ProjectDir == "" {
		return errors.New("ProjectDir is not set")
	}
	// Check that the project dir exists and can be accessed
	_, err := os.Stat(opts.ProjectDir)
	if err != nil {
		return errors.WithStack(err)
	}
	return nil
}

type Builder struct {
	*BuilderOptions
}

func NewBuilder(opts *BuilderOptions) (*Builder, error) {
	err := opts.Validate()
	if err != nil {
		return nil, err
	}

	b := &Builder{BuilderOptions: opts}

	return b, err
}

func (b *Builder) Build(targetClass string, targetMethod string) (*build.Result, error) {
	var flags []string
	if b.Parallel.Enabled {
		flags = append(flags, "-T")
		if b.Parallel.NumJobs != 0 {
			flags = append(flags, fmt.Sprint(b.Parallel.NumJobs))
		} else {
			// Use one thread per cpu core
			flags = append(flags, "1C")
		}
	}
	args := append(flags, "test-compile")

	err := runMaven(b.ProjectDir, args, b.Stderr, b.Stderr)
	if err != nil {
		return nil, err
	}

	project, err := parsePomXML(b.ProjectDir)
	if err != nil {
		return nil, err
	}

	deps, err := b.getExternalDependencies()
	if err != nil {
		return nil, err
	}
	// Append local dependencies which are not listed by "mvn dependency:build-classpath"
	// These directories are configurable
	deps = append(deps, []string{
		project.Build.OutputDirectory,
		project.Build.TestOutputDirectory,
	}...)

	buildDir := project.Build.Directory

	result := &build.Result{
		Name:         targetClass,
		TargetMethod: targetMethod,
		BuildDir:     buildDir,
		ProjectDir:   b.ProjectDir,
		RuntimeDeps:  deps,
	}

	return result, nil
}

func (b *Builder) getExternalDependencies() ([]string, error) {
	tempDir, err := os.MkdirTemp("", "cifuzz-maven-dependencies-*")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer fileutil.Cleanup(tempDir)

	outputPath := filepath.Join(tempDir, "cp")
	outputFlag := "-Dmdep.outputFile=" + outputPath

	args := []string{
		"dependency:build-classpath",
		outputFlag,
	}

	err = runMaven(b.ProjectDir, args, b.Stderr, b.Stderr)
	if err != nil {
		return nil, err
	}

	output, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	deps := strings.Split(strings.TrimSpace(string(output)), string(os.PathListSeparator))
	return deps, nil
}

func runMaven(projectDir string, args []string, stdout, stderr io.Writer) error {
	// always run it with the cifuzz profile
	args = append(args, "-Pcifuzz")
	// remove color from output
	args = append(args, "-B")
	cmd := exec.Command(
		"mvn",
		args...,
	)
	// Redirect the command's stdout to stderr to only have
	// reports printed to stdout
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = projectDir
	log.Debugf("Working directory: %s", cmd.Dir)
	log.Debugf("Command: %s", cmd.String())
	err := cmd.Run()
	if err != nil {
		return cmdutils.WrapExecError(errors.WithStack(err), cmd)
	}

	return nil
}

func parsePomXML(projectDir string) (*Project, error) {
	args := []string{
		"help:evaluate",
		"-Dexpression=project",
		"-DforceStdout",
		"--quiet",
	}
	stdout := new(bytes.Buffer)
	err := runMaven(projectDir, args, stdout, stdout)
	if err != nil {
		return nil, err
	}

	project, err := parseXML(stdout)
	if err != nil {
		return nil, err
	}

	return project, nil
}

// GetTestDir returns the value of <testSourceDirectory> from the projects
// pom.xml as an absolute path.
// Note: If no tag is specified, the parser will return the
// default value "projectDir/src/test/java".
func GetTestDir(projectDir string) (string, error) {
	project, err := parsePomXML(projectDir)
	if err != nil {
		return "", errors.Wrap(err, "Failed to get test directory of project")
	}

	log.Debugf("Found maven test source at: %s", project.Build.TestSourceDirectory)
	return strings.TrimSpace(project.Build.TestSourceDirectory), nil
}

// GetSourceDir returns the value of <sourceDirectory> from the projects
// pom.xml as an absolute path.
// Note: If no tag is specified, the parser will return the
// default value "projectDir/src/main/java".
func GetSourceDir(projectDir string) (string, error) {
	project, err := parsePomXML(projectDir)
	if err != nil {
		return "", errors.Wrap(err, "Failed to get source directory of project")
	}

	log.Debugf("Found maven source at: %s", project.Build.SourceDirectory)
	return strings.TrimSpace(project.Build.SourceDirectory), nil
}
