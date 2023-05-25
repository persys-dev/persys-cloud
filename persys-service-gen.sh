#!/bin/bash

# Set default values
go_mod=""
project_name=""
no_git=false
open_in_vscode=false

# Parse command line arguments
while [[ $# -gt 0 ]]
do
    key="$1"

    case $key in
        --go-mod | -g)
            go_mod="$2"
            shift # past argument
            shift # past value
            ;;
        --project-name | -p)
            project_name="$2"
            shift # past argument
            shift # past value
            ;;
        --code | -c)
            open_in_vscode=true
            shift # past argument
            ;;
        --no-git | -ng)
            no_git=true
            shift
            ;;
        *)    # unknown option
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Check required arguments
if [ -z "$go_mod" ]; then
    echo "Go module name (--go-mod or -g) is required"
    exit 1
fi

if [ -z "$project_name" ]; then
    echo "Project name (--project-name or -p) is required"
    exit 1
fi

# Create the base directory
mkdir "$project_name"
cd "$project_name" || exit

# Create the cmd directory and main.go file
mkdir cmd
touch cmd/main.go

# Create the config directory, config.go file, and Dockerfile
mkdir config
touch config/config.go

# Create the Internal directory

mkdir internal

# Create the test directory and adding suite_test.go
mkdir test
touch test/suite_test.go

# Create models Directory

mkdir models

# Create Utils Directory

mkdir utils

# Create config.toml

touch config.toml

# Create Docker File
touch Dockerfile

# Create the README.md file
touch README.md

# Initialize a Git repository
if [ "$no_git" = false ]; then
  git init
fi

# Initialize a Go module with the provided package name
go mod init "$go_mod"

# Open in VS Code if flag is set
if [ "$open_in_vscode" = true ]; then
    code .
fi