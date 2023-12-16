# PerSys Cloud automation software

PerSys Cloud CI/CD Tool is an automation software for managing a multi-environment CI/CD pipeline.

## Prerequisites

Before using the PerSys Cloud CI/CD Tool, make sure you have the following tools installed on your system:

- Git
- Docker
- Helm
- kubectl

## Installation

1. Clone the PerSys Cloud automation Tool repository:
   ````shell
   git clone https://github.com/persys-dev/persys-cloud.git
    ```
2. Change to the tool's directory:
   ````shell
   cd build/cmd
   ````
3. Build the tool:
   ````shell
   go build
   ````
4. Set the tool as executable:
   ````shell
   chmod +x pcas
   ````
## Usage

### Build Go Binaries

Build Go binaries with no output for checking if compiles without error.
````shell
./pcas build-binary
````
### Build Docker Images

Build Docker images for services.
````shell
./pcas build-docker
````
### Git Commands

Perform Git commands such as listing changes, staging changes, committing changes, and displaying Git information.

#### List Changes

List changes in the repository.
````shell
./pcas git list-changes
````
#### Stage Changes

Stage changes in the repository.
````shell
./pcas git stage-changes
````
#### Commit Changes

Commit staged changes in the repository.
````shell
./pcas git commit-changes
````
#### Display Git Information

Display Git information.
````shell
./pcas git info
````
### Check Log File

Check the the Log File.
````shell
./pcas status
````
## Disclaimer

Please note that the PerSys Cloud CI/CD Tool requires Git, Docker, Helm, and kubectl to be installed on your system. Make sure you have these tools installed and properly configured before using the tool.

## Use Cases
1. **Automated Build Process**: The tool allows you to automate the build process for your services. You can use the tool to build binaries and Docker images for your services, making it easier to manage and deploy your applications.

2. **Webhook Handler**: The tool provides a webhook handler for your GitOps pipeline. This allows you to trigger the build process for your services.

3. **Cloud Provider Integration**: The tool allows you to integrate your services with the cloud provider. This allows you to deploy your services to different cloud providers.

4. **Continues Delivery**: The tool provides a continuous delivery pipeline for your services. This allows you to deploy your services to different cloud providers.

5. **Service Validation**: With the "build-binary" command, you can quickly build Go binaries for your services without any output. This can be useful for validating the services and ensuring they are functioning correctly before proceeding with further steps in your CI/CD pipeline.

6. **Docker Image Creation**: The "build-docker" command enables you to build Docker images for your services. This simplifies the process of containerizing your applications and ensures consistency in deployment across different environments.

7. **Git Integration**: The tool provides Git commands to help you manage your repository. You can use the "list-changes" command to view the changes in your repository, the "stage-changes" command to stage changes for commit, and the "commit-changes" command to commit the staged changes. Additionally, the "git-info" command displays Git information such as the branch, last commit, and any unstaged or uncommitted changes.

8. **Build Status Monitoring**: The "status" command allows you to check the build status by displaying the contents of the build log file. This provides visibility into the success or failure of the build process, making it easier to identify and resolve any issues.

9. **Granular Control**: The tool provides a granular control for your services. This allows you to control the build process for your services, making it easier to manage and deploy your applications.
    
## License

This project is licensed under the [MIT License](LICENSE).

