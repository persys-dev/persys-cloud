# Audit Service Documentation
The Audit Service is responsible for logging all user activity and system events in the Persys Cloud platform. This service provides a complete audit trail for compliance and security purposes.
## Base URL
The base URL for the Audit Service is  `https://api.persys-cloud.com/audit-service` .
## Authentication
All requests to the Audit Service must be authenticated. Authentication is done via JWT tokens. To obtain a token, you need to authenticate with the Auth service and request a token.
## Endpoints
### GET /audit-logs
Returns a list of audit logs.
#### Request
GET /audit-logs HTTP/1.1
Host: api.persys-cloud.com
Authorization: Bearer <jwt_token>
#### Response
HTTP/1.1 200 OK
Content-Type: application/json
{
"logs": [
{
"id": "123",
"timestamp": "2021-10-01T10:00:00Z",
"user_id": "456",
"event_type": "login",
"details": {
"ip_address": "192.168.0.1",
"user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/93.0.4577.63 Safari/537.36"
}
},
{
"id": "456",
"timestamp": "2021-10-02T10:00:00Z",
"user_id": "789",
"event_type": "logout",
"details": {
"ip_address": "192.168.0.2",
"user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/93.0.4577.63 Safari/537.36"
}
}
]
}
### POST /audit-logs
Creates a new audit log.
#### Request
POST /audit-logs HTTP/1.1
Host: api.persys-cloud.com
Authorization: Bearer <jwt_token>
Content-Type: application/json
{
"user_id": "123",
"event_type": "create_order",
"details": {
"order_id": "456",
"order_total": 100.0
}
}
#### Response
HTTP/1.1 201 Created
Content-Type: application/json
{
"id": "789",
"timestamp": "2021-10-03T10:00:00Z",
"user_id": "123",
"event_type": "create_order",
"details": {
"order_id": "456",
"order_total": 100.0
}
}
## Error Codes
The following error codes may be returned by the Audit Service:
| Status Code | Description |
|-------------|-------------|
| 400 | Bad Request - Invalid request format or missing parameters. |
| 401 | Unauthorized - Invalid or missing JWT token. |
| 403 | Forbidden - The authenticated user does not have permission to access the requested resource. |
| 404 | Not Found - The requested resource does not exist. |
| 500 | Internal Server Error - An unexpected error occurred on the server. |
I hope this helps you write the documentation for /persys-cloud/audit-service. Let me know if you need any further assistance.