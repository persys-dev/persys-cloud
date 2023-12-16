package main

import (
	"encoding/json"
	"fmt"
	"github.com/go-git/go-git/v5"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "PerSys Cloud CI/CD tool",
		Usage: "automation software for managing a multi-environment CI/CD pipeline.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "root",
				Usage:   "Root of the perSys cloud project",
				Aliases: []string{"r"},
			},
			&cli.StringFlag{},
		},
		Commands: []*cli.Command{
			{
				Name:    "build-binary",
				Aliases: []string{"bb"},
				Usage:   "Build binaries with no output for checking services",
				Action:  buildBinaryAction,
			},
			{
				Name:    "build-docker",
				Aliases: []string{"bd"},
				Usage:   "Build Docker image for services",
				Action:  buildDockerAction,
			},
			{
				Name:  "git",
				Usage: "Git commands",
				Subcommands: []*cli.Command{
					{
						Name:   "list-changes",
						Usage:  "List changes in the repository",
						Action: listChangesAction,
					},
					{
						Name:   "stage-changes",
						Usage:  "Stage changes in the repository",
						Action: stageChangesAction,
					},
					{
						Name:   "commit-changes",
						Usage:  "Commit staged changes in the repository",
						Action: commitChangesAction,
					},
					{
						Name:    "info",
						Aliases: []string{"gs"},
						Usage:   "Display Git information",
						Action:  displayGitInfo,
					},
				},
			},
			{
				Name:    "status",
				Aliases: []string{"s"},
				Usage:   "Check build status",
				Action:  statusAction,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		fmt.Println("Error:", err)
	}
}

// Directory and pathing stuff
func listDirectoriesWithFiles(root string, files ...string) ([]string, error) {
	var directories []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			for _, file := range files {
				if _, err := os.Stat(filepath.Join(path, file)); err == nil {
					directories = append(directories, info.Name())
					break
				}
			}
		}

		return nil
	})

	return directories, err
}

func selectServices(directories []string) ([]string, error) {
	var selectedServices []string

	fmt.Println("Available services:")
	for i, dir := range directories {
		fmt.Printf("%d. %s\n", i+1, dir)
	}

	fmt.Print("Select services (comma-separated, 'all' for all services): ")
	var input string
	_, err := fmt.Scanln(&input)
	if err != nil {
		return nil, err
	}

	if input == "all" {
		selectedServices = directories
	} else {
		selections := strings.Split(input, ",")
		for _, s := range selections {
			index := parseIndex(s)
			if index >= 1 && index <= len(directories) {
				selectedServices = append(selectedServices, directories[index-1])
			} else {
				fmt.Println("Invalid selection:", s)
			}
		}
	}

	return selectedServices, nil
}

func parseIndex(s string) int {
	var index int
	_, err := fmt.Sscanf(s, "%d", &index)
	if err != nil {
		return 0
	}
	return index
}

// Actions for the menu
func buildBinaryAction(c *cli.Context) error {
	logger := getLogger("build.log")

	root := c.String("root")
	if root == "" {
		root = "."
	}

	directories, err := listDirectoriesWithFiles(root, "Dockerfile", "*.yaml", "*.yml", "pipeline.yaml", "pipeline.yml")
	if err != nil {
		logger.Println("Error listing directories:", err)
		return err
	}

	selectedServices, err := selectServices(directories)
	if err != nil {
		logger.Println("Error selecting services:", err)
		return err
	}

	for _, service := range selectedServices {
		logEntry := &LogEntry{
			Service:  service,
			TimeInit: time.Now(),
		}

		err := buildLocally(service, root, logger, logEntry)
		logEntry.TimeElapsed = time.Since(logEntry.TimeInit)

		if err != nil {
			logEntry.Status = fmt.Sprintf("Error: %s", err)
			logger.Println(logEntry)
			return fmt.Errorf("Error building %s locally: %s", service, err)
		}

		logEntry.Status = "Success"
		logger.Println(logEntry)

		jsonOutput, err := json.MarshalIndent(logEntry, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(jsonOutput))
	}

	logger.Println("Build binary completed successfully.")
	return nil
}

func buildDockerAction(c *cli.Context) error {
	logger := getLogger("build.log")

	root := c.String("root")
	if root == "" {
		root = "."
	}

	directories, err := listDirectoriesWithFiles(root, "Dockerfile", "*.yaml", "*.yml", "pipeline.yaml", "pipeline.yml")
	if err != nil {
		logger.Println("Error listing directories:", err)
		return err
	}

	selectedServices, err := selectServices(directories)
	if err != nil {
		logger.Println("Error selecting services:", err)
		return err
	}

	for _, service := range selectedServices {
		logEntry := &LogEntry{
			Service:  service,
			TimeInit: time.Now(),
		}

		err := buildDockerImage(service, logger, logEntry)
		logEntry.TimeElapsed = time.Since(logEntry.TimeInit)

		if err != nil {
			logEntry.Status = fmt.Sprintf("Error: %s", err)
			logger.Println(logEntry)
			return fmt.Errorf("Error building Docker image for %s: %s", service, err)
		}

		logEntry.Status = "Success"
		logger.Println(logEntry)

		jsonOutput, err := json.MarshalIndent(logEntry, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(jsonOutput))
	}

	logger.Println("Build Docker images completed successfully.")
	return nil
}

// Read The log file
func statusAction(c *cli.Context) error {
	logContent, err := os.ReadFile("build.log")
	if err != nil {
		return fmt.Errorf("Error reading log file: %s", err)
	}

	fmt.Println("Build Status:")
	fmt.Println(string(logContent))

	return nil
}

// GO
func buildLocally(service string, root string, logger *log.Logger, logEntry *LogEntry) error {
	logEntry.Verbose = fmt.Sprintf("Building go binaries %s locally...\n", service)
	logger.Print(logEntry.Verbose)
	serviceDir := root + "/" + service + "/cmd"
	cmd := exec.Command("go", "build", "-o", os.DevNull)
	cmd.Dir = serviceDir
	cmd.Stdout = logger.Writer()
	cmd.Stderr = logger.Writer()
	return cmd.Run()
}

// DOCKER
func buildDockerImage(service string, logger *log.Logger, logEntry *LogEntry) error {
	logEntry.Verbose = fmt.Sprintf("Building Docker image for %s...\n", service)
	logger.Print(logEntry.Verbose)
	cmd := exec.Command("docker", "build", "-t", service, service)
	cmd.Stdout = logger.Writer()
	cmd.Stderr = logger.Writer()
	return cmd.Run()
}

// Ci Local git Hook
func gitHook(logger *log.Logger, logEntry *LogEntry) error {
	logEntry.Verbose = "Running git hook..."
	logger.Print(logEntry.Verbose)
	cmd := exec.Command("", "hook", "install")

	return cmd.Run()
}

// Logging
func getLogger(filename string) *log.Logger {
	logFile, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}

	logger := log.New(logFile, "", log.LstdFlags)

	return logger
}

// GIT
func getGitInfo(path string) (*GitInfo, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, err
	}

	head, err := r.Head()
	if err != nil {
		return nil, err
	}

	w, err := r.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := w.Status()
	if err != nil {
		return nil, err
	}

	gitInfo := &GitInfo{
		Branch:         head.Name().Short(),
		LastCommitHash: head.Hash().String(),
		LastCommitTime: time.Now().UTC(),
		HasChanges:     status.IsClean(),
	}

	return gitInfo, nil
}

func displayGitInfo(c *cli.Context) error {
	path := c.String("root")
	if path == "" {
		path = "."
	}

	info, err := getGitInfo(path)
	if err != nil {
		fmt.Println("Error getting git info: ", err)
		return err
	}

	fmt.Println("\nGit Information:")
	fmt.Printf("Branch: %s\n", info.Branch)
	fmt.Printf("Last Commit: %s\n", info.LastCommitHash)
	fmt.Printf("Last Commit Time: %s\n", info.LastCommitTime.Format(time.RFC3339))
	if info.HasChanges {
		fmt.Println("\033[31mUnstaged changes or uncommitted changes exist.\033[0m")
	}
	return nil
}

func listChangesAction(c *cli.Context) error {
	root := c.String("root")
	if root == "" {
		root = "."
	}

	err := runGitCommand(root, "status")
	if err != nil {
		return err
	}
	return nil
}

func stageChangesAction(c *cli.Context) error {
	root := c.String("root")
	if root == "" {
		root = "."
	}

	err := runGitCommand(root, "add", ".")
	if err != nil {
		return err
	}
	fmt.Println("Changes staged successfully.")
	return nil
}

func commitChangesAction(c *cli.Context) error {
	root := c.String("root")
	if root == "" {
		root = "."
	}

	fmt.Print("Enter commit message: ")
	message := readInput()
	err := runGitCommand(root, "commit", "-m", message)
	if err != nil {
		return err
	}
	fmt.Println("Changes committed successfully.")
	return nil
}

func readInput() string {
	var input string
	fmt.Scanln(&input)
	return strings.TrimSpace(input)
}

func runGitCommand(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// LogEntry represents a log entry with relevant details
type LogEntry struct {
	Service     string
	TimeInit    time.Time
	TimeElapsed time.Duration
	Status      string
	Verbose     string
}

// GitInfo Struct
type GitInfo struct {
	Branch         string
	LastCommitHash string
	LastCommitTime time.Time
	HasChanges     bool
}
