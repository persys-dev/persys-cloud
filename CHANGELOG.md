# Persys Cloud Changelog

## [Unreleased] - 2024-01-XX

### 🚀 Major Features Added

#### **Comprehensive Reconciliation System**
- **New File**: `prow/internal/scheduler/reconciler.go`
  - Implemented full reconciliation engine for workload state management
  - Added desired state vs actual state comparison
  - Automatic recovery actions: recreate, restart, stop workloads
  - Continuous reconciliation loop (runs every 2 minutes)
  - Reconciliation statistics and metadata tracking

#### **Enhanced API Gateway CoreDNS Discovery**
- **Modified**: `api-gateway/services/prow.service.go`
  - Added `DiscoverSchedulers()` method for dynamic scheduler discovery
  - Implemented `queryCoreDNSForSchedulers()` for SRV and A record lookup
  - Added support for multiple scheduler addresses
  - Fallback to configured scheduler address if discovery fails

#### **Reconciliation API Endpoints**
- **New File**: `prow/internal/api/reconciliation_handlers.go`
  - `GET /reconciliation/stats` - Get reconciliation statistics
  - `POST /reconciliation/trigger` - Manually trigger reconciliation cycle
  - `POST /workloads/{id}/reconcile` - Reconcile specific workload
  - `PUT /workloads/{id}/desired-state` - Update workload desired state

#### **API Gateway Proxy System**
- **Modified**: `api-gateway/services/prow.service.go`
  - Implemented comprehensive proxy functionality for prow-scheduler communication
  - Added mTLS communication with prow schedulers
  - Proper header forwarding and authentication handling
  - Removed HMAC logic (HMAC only between prow and persys-agent)
  - Added request/response copying utilities

#### **Route Separation and Security**
- **Modified**: `prow/internal/api/handlers.go`
  - Separated mTLS and non-mTLS routes into different files
  - **New File**: `prow/internal/api/mtls_handlers.go` - mTLS-only endpoints
  - **New File**: `prow/internal/api/nonmtls_handlers.go` - Non-mTLS endpoints
  - Fixed route registration issues causing 404 errors
  - Proper authentication method separation

#### **Enhanced Monitoring System**
- **Modified**: `prow/internal/scheduler/monitor.go`
  - Added log fetching from agents when container status changes
  - Integrated with existing polling-based monitoring
  - Enhanced container status mapping and log capture
  - Improved workload status tracking in etcd

#### **Persys-Agent Async Execution Support**
- **Modified**: `persys-agent/src/routes/DockerRoutes.cpp`
  - Implemented async Docker container execution using std::thread
  - Added workload ID and display name support
  - Enhanced container labeling for tracking
  - Immediate response with "Command queued for execution"
  - Proper error handling and logging for async operations

- **Modified**: `persys-agent/src/controllers/DockerController.cpp`
  - Enhanced startContainer method with workload ID support
  - Added display name labeling for container identification
  - Improved error handling and logging
  - Support for async execution patterns

- **Modified**: `persys-agent/src/routes/ComposeRoutes.cpp`
  - Enhanced compose execution with async support
  - Added workload ID tracking for compose operations
  - Improved error handling and response formatting
  - Support for environment variables and authentication

- **Modified**: `persys-agent/src/controllers/ComposeController.cpp`
  - Enhanced repository cloning with authentication support
  - Improved compose file detection and execution
  - Added build option detection for Dockerfiles
  - Better error handling and directory management

### 🔧 Configuration & Structure Changes

#### **API Gateway Configuration Refactor**
- **Modified**: `api-gateway/cmd/main.go`
  - Fixed config structure mismatch (flat vs nested fields)
  - Updated to use `config.LoadConfig()` instead of non-existent `ReadConfig()`
  - Fixed all config field references to use correct nested structure:
    - `cnf.TLS.CAPath` instead of `cnf.TLSCAPath`
    - `cnf.CoreDNS.Addr` instead of `cnf.CoreDNSAddr`
    - `cnf.GitHub.WebHookURL` instead of `cnf.WebHookURL`
    - `cnf.Database.MongoURI` instead of `cnf.MongoURI`
    - `cnf.App.HTTPAddr` instead of `cnf.HttpAddr`
  - Added CoreDNS discovery integration
  - Enhanced error handling and logging

#### **Enhanced Workload Model**
- **Modified**: `prow/internal/models/models.go`
  - Added `DesiredState` field for reconciliation state management
  - Added `Metadata` field for reconciliation tracking and statistics
  - Maintains backward compatibility with existing workloads

#### **Scheduler Architecture Enhancement**
- **Modified**: `prow/internal/scheduler/scheduler.go`
  - Integrated monitor and reconciler into scheduler struct
  - Added `StartMonitoring()` method for combined monitoring
  - Added `StartReconciliation()` method for reconciliation loop
  - Added public methods for reconciliation access
  - Set default desired state to "Running" for new workloads
  - Enhanced async workload execution
  - Improved error handling and logging

#### **Prow Scheduler Main Application**
- **Modified**: `prow/cmd/scheduler/main.go`
  - Updated to use separated route handlers
  - Enhanced monitoring and reconciliation startup
  - Improved error handling and logging
  - Added proper shutdown handling

### 🔄 System Integration Improvements

#### **API Gateway Service Discovery**
- **Enhanced**: `api-gateway/services/prow.service.go`
  - Dynamic scheduler discovery via CoreDNS
  - Support for multiple scheduler endpoints
  - Automatic fallback mechanisms
  - TLS-enabled communication with discovered schedulers
  - Comprehensive proxy functionality

#### **Controller Configuration Updates**
- **Modified**: `api-gateway/controllers/auth.controller.go`
  - Fixed GitHub OAuth config references
  - Updated to use `config.LoadConfig()`
  - Fixed client ID and secret field paths

- **Modified**: `api-gateway/controllers/github.controller.go`
  - Fixed webhook URL and secret field paths
  - Updated to use nested config structure

- **Modified**: `api-gateway/controllers/prow.controller.go`
  - Enhanced proxy request handling
  - Improved error handling and logging
  - Added request/response copying utilities

#### **Utility Configuration Fixes**
- **Modified**: `api-gateway/utils/audit.go`
  - Fixed log level and Loki endpoint field paths
  - Updated service name references
  - Fixed config structure compatibility

#### **Route Management**
- **Modified**: `api-gateway/routes/prow.routes.go`
  - Enhanced route handling for proxy functionality
  - Improved error handling and logging
  - Added proper request forwarding

### 🐛 Bug Fixes

#### **Configuration Structure Mismatch**
- **Root Cause**: API gateway was using old flat config structure while config.go used nested structure
- **Fix**: Updated all config references throughout the codebase
- **Impact**: Resolves startup failures and missing configuration values

#### **Missing CoreDNS Discovery**
- **Root Cause**: `DiscoverSchedulers` method was called but not implemented
- **Fix**: Implemented complete CoreDNS discovery mechanism
- **Impact**: Enables dynamic scheduler discovery instead of static configuration

#### **Reconciliation Bug Fix**
- **Root Cause**: The reconciliation loop was not correctly updating workload states or handling certain edge cases, leading to inconsistencies between desired and actual workload states.
- **Fix**: Refactored the reconciliation logic to ensure accurate state comparison, proper update of workload status, and robust handling of all reconciliation actions (recreate, restart, stop).
- **Impact**: Ensures the reconciliation system maintains state consistency and self-healing as intended, with improved reliability and observability.

#### **Workload State Management**
- **Root Cause**: No mechanism to track desired vs actual workload state
- **Fix**: Implemented comprehensive reconciliation system
- **Impact**: Enables self-healing and state consistency

#### **Route Registration Issues**
- **Root Cause**: mTLS and non-mTLS routes were mixed, causing 404 errors
- **Fix**: Separated routes into different files and proper registration
- **Impact**: Resolves 404 errors and proper authentication handling

#### **HTTP Timeout Issues**
- **Root Cause**: Synchronous Docker command execution causing timeouts
- **Fix**: Implemented async execution model in persys-agent
- **Impact**: Prevents timeouts and improves system responsiveness
- **Implementation**: Used std::thread for background execution with immediate response

### 📊 Monitoring & Observability

#### **Enhanced Monitoring System**
- **Modified**: `prow/internal/scheduler/monitor.go`
  - Integrated with reconciliation system
  - Improved container status mapping
  - Enhanced log capture and status tracking
  - Added log fetching from agents

#### **Reconciliation Statistics**
- **Added**: Comprehensive statistics tracking
  - Total workloads processed
  - Workloads needing reconciliation
  - Reconciliation errors and success rates
  - Action counts (recreate/restart/stop)
  - Timestamp tracking for all operations

#### **Metrics and Prometheus**
- **Enhanced**: Prometheus metrics availability on non-mTLS server
- **Added**: Metrics for all routes including mTLS-only routes
- **Improved**: Monitoring and observability capabilities

### 🔐 Security & Authentication

#### **Maintained Security Model**
- **Preserved**: mTLS communication between API gateway and prow
- **Preserved**: HMAC authentication between prow and persys-agent
- **Preserved**: OAuth authentication for client access
- **Enhanced**: Secure reconciliation operations with proper authentication
- **Clarified**: Authentication method separation (mTLS vs HMAC)

### 🚀 Performance & Scalability

#### **Async Workload Execution**
- **Enhanced**: Workload scheduling now uses async execution
- **Benefit**: Prevents HTTP timeouts during long-running operations
- **Benefit**: Improved system responsiveness
- **Implementation**: 
  - Prow scheduler sends async commands to persys-agent
  - Persys-agent uses std::thread for background execution
  - Immediate response with "Command queued for execution"
  - Background execution with proper error handling

#### **Efficient State Management**
- **Added**: Optimized reconciliation cycles
- **Benefit**: Reduces unnecessary operations
- **Benefit**: Better resource utilization

#### **Workload ID Consistency**
- **Enhanced**: Use workload ID as container name for consistency
- **Added**: Original workload name as label for display
- **Improved**: Container matching and tracking
- **Implemented**: In persys-agent Docker and Compose controllers

### 📝 API Changes

#### **New Endpoints**
```
GET    /reconciliation/stats
POST   /reconciliation/trigger
POST   /workloads/{id}/reconcile
PUT    /workloads/{id}/desired-state
```

#### **Enhanced Endpoints**
- All existing endpoints maintain backward compatibility
- Added reconciliation metadata to workload responses
- Enhanced error handling and logging
- Improved proxy functionality

### 🔧 Development & Build

#### **Build System**
- **Fixed**: All compilation errors resolved
- **Enhanced**: Proper dependency management
- **Improved**: Error handling and logging throughout

#### **Code Quality**
- **Added**: Comprehensive error handling
- **Enhanced**: Logging and debugging capabilities
- **Improved**: Code organization and structure

### 📋 Migration Notes

#### **For Existing Deployments**
1. **Configuration**: Update config.toml to use new nested structure (if not already done)
2. **Database**: No migration required - new fields are optional
3. **API**: All existing API endpoints remain compatible
4. **Monitoring**: New reconciliation metrics available via API
5. **Routes**: Ensure proper route registration for mTLS/non-mTLS separation

#### **For New Deployments**
1. **CoreDNS**: Ensure CoreDNS is properly configured for service discovery
2. **Reconciliation**: Enable reconciliation loop in scheduler startup
3. **Monitoring**: Configure monitoring for reconciliation statistics
4. **Authentication**: Verify mTLS and HMAC configurations

### 🎯 Impact Summary

#### **High Impact Changes**
- ✅ **Self-Healing System**: Automatic workload recovery
- ✅ **Dynamic Discovery**: No more static scheduler configuration
- ✅ **State Consistency**: Guaranteed workload state management
- ✅ **Enhanced Observability**: Complete visibility into system state
- ✅ **Route Separation**: Fixed authentication and routing issues
- ✅ **Async Execution**: Resolved timeout and performance issues

#### **Medium Impact Changes**
- ✅ **Configuration Stability**: Fixed config structure issues
- ✅ **API Enhancement**: New management endpoints
- ✅ **Performance**: Async execution and optimized monitoring
- ✅ **Proxy System**: Enhanced API gateway functionality

#### **Low Impact Changes**
- ✅ **Code Quality**: Better error handling and logging
- ✅ **Maintainability**: Improved code organization
- ✅ **Documentation**: Enhanced inline documentation

### 📁 Complete File Change Summary

#### **New Files Created (4)**
1. `prow/internal/scheduler/reconciler.go`
2. `prow/internal/api/reconciliation_handlers.go`
3. `prow/internal/api/mtls_handlers.go`
4. `prow/internal/api/nonmtls_handlers.go`

#### **Modified Files (20+)**
1. `api-gateway/cmd/main.go`
2. `api-gateway/config/config.go`
3. `api-gateway/config.toml`
4. `api-gateway/services/prow.service.go`
5. `api-gateway/controllers/auth.controller.go`
6. `api-gateway/controllers/github.controller.go`
7. `api-gateway/controllers/prow.controller.go`
8. `api-gateway/routes/prow.routes.go`
9. `api-gateway/utils/audit.go`
10. `prow/internal/models/models.go`
11. `prow/internal/scheduler/scheduler.go`
12. `prow/internal/scheduler/monitor.go`
13. `prow/internal/scheduler/dns.go`
14. `prow/internal/api/handlers.go`
15. `prow/cmd/scheduler/main.go`
16. `persys-agent/src/routes/DockerRoutes.cpp`
17. `persys-agent/src/controllers/DockerController.cpp`
18. `persys-agent/src/routes/ComposeRoutes.cpp`
19. `persys-agent/src/controllers/ComposeController.cpp`
20. `CHANGELOG.md` (this file)

#### **Configuration Files**
- Updated configuration structures and field references
- Enhanced service discovery settings
- Improved authentication configurations

### 🔒 Certificate SAN Support for mTLS
- **Enhanced**: Both `prow` and `api-gateway` now automatically include Subject Alternative Names (SANs) in their mTLS certificate requests to CFSSL.
  - Certificates are now valid for `localhost`, the service name (e.g., `prow`, `api-gateway`), and `127.0.0.1`.
  - **Modified**: `prow/internal/auth/certmanager.go`, `api-gateway/services/certmanager.go`
  - **Impact**: Fixes TLS errors for local development and secure connections, allowing clients to connect using `localhost`, service DNS, or IP without certificate validation errors.

---

### 🛠️ CoreDNS & Service Discovery Troubleshooting

#### **Recent Lessons and Improvements (2024-06)**
- **CoreDNS/etcd Integration:**
  - Ensured CoreDNS is configured to serve the correct zone (e.g., `persys.local`) from etcd, matching the service registration and discovery domains.
  - Verified that SRV and A records are correctly written to etcd and that CoreDNS is able to return them for service discovery queries.
  - Updated Corefile configuration to explicitly include the correct zone and etcd endpoints, resolving issues where records were present in etcd but not returned by CoreDNS.
  - Added the command to CoreDNS to use the correct config files
  - Exposed CoreDNS prometheus metrics
- **API Gateway & Prow Discovery Logic:**
  - Improved the API Gateway's service discovery logic to use CoreDNS for dynamic scheduler discovery, with robust fallback to static configuration if discovery fails.
  - Fixed the parsing of SRV records so that the SRV target is resolved to an IP address before establishing mTLS connections, preventing connection errors when using service names.
  - Enhanced error handling and logging for DNS and service discovery failures, making troubleshooting easier.
  - Added configuration options for `DiscoveryDomain` and `DiscoveryService` in the API Gateway for flexible discovery setup.
- **General Troubleshooting Steps:**
  - Emphasized the importance of matching the domain used in both service registration (by prow) and discovery (by api-gateway).
  - Documented the process of checking etcd keys, CoreDNS logs, and DNS queries to ensure end-to-end service discovery is functioning as expected.

These changes and lessons have resulted in a more robust, production-ready service discovery system, with clear troubleshooting steps and improved observability for future debugging.

---

### 🚀 Observability & Instrumentation Improvements (2024-06)

- **Full OpenTelemetry Tracing:**
  - All outgoing HTTP requests in both `prow` and `api-gateway` are now instrumented with OpenTelemetry, including:
    - Agent API calls
    - Handshake requests
    - Certificate signing requests to CFSSL
  - All distributed traces are now visible in your tracing backend (e.g., Jaeger).
- **Prometheus Metrics:**
  - The API Gateway now exposes Prometheus metrics for all endpoints, including mTLS traffic, by instrumenting both mTLS and non-mTLS routers.
- **Reconciliation & Monitoring Bugfixes:**
  - Fixed grace period logic in the reconciler to prevent runaway container launches.
  - Improved container state reporting and monitoring, ensuring accurate status (Running, Exited, Pulling, etc.) is always reported and visible in both etcd and the UI.
- **Certificate Management:**
  - CertificateManager in both services now includes OpenTelemetry tracing for all certificate requests, improving visibility into certificate provisioning and renewal flows.

---

## Previous Versions

*Note: This changelog covers the comprehensive reconciliation, discovery, proxy, and routing system implementation. Previous changes are documented in individual component histories.* 

### 🆕 Service Identification Header
- **Added**: All HTTP responses from both `api-gateway` and `prow-scheduler` now include an `X-Service-Name` response header for service identification by clients.
  - Implemented via Gin middleware in both services.
  - Header value is `api-gateway` for the API gateway and `prow-scheduler` for the scheduler. 

### 🛠️ Persys-Agent Enhancements
- **Async Docker Execution**: Docker container runs are now executed asynchronously using `std::thread`, allowing immediate API responses and preventing timeouts.
- **Command Argument Support**: The agent now supports passing and executing custom container commands via the API, ensuring user-supplied commands are honored.
- **Improved Error Handling**: Enhanced error handling and logging for async operations and Docker execution failures.
- **Container Labeling**: Containers are now labeled with both workload ID and display name for improved tracking and management. 