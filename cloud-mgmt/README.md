## cloud management service

#### this service handles kubernetes clusters for clients using automation and is the heart of the project.

### Providers
we currently support the following cloud providers:
* Amazon Web Services
* Microsoft Azure
* Google Cloud platform
* PerSys Cloud

### automating infrastructure installation / configuration

this uses an ansible play book to automate configuring vmware vCenter and then installing the Cluster-API CRD.

play book is located at : /IaC/ansible/setup-vsphere-provider.