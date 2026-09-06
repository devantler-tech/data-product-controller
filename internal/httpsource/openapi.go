package httpsource

// openAPI describes only the fixed public interface; upstream connection details are private.
const openAPI = `{
	"openapi": "3.1.0",
	"info": {"title": "Read-only JSON export", "version": "1.0.0"},
	"paths": {
		"/api/data": {
			"get": {
				"operationId": "readExport",
				"summary": "Read the configured JSON export",
				"description": "Returns one complete JSON value, at most 1 MiB. Query parameters and request bodies are rejected. Access is controlled by the deployment's private ingress boundary.",
				"responses": {
					"200": {"description": "Complete source export", "content": {"application/json": {"schema": {}}}},
					"400": {"description": "Query parameters or a body were supplied"},
					"404": {"description": "The connector release feature is disabled"},
					"502": {"description": "The source is unavailable or returned an invalid export"},
					"503": {"description": "Configuration is unavailable or query capacity is exhausted"}
				}
			}
		}
	}
}`
