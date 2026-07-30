# Content Pipeline

A secure, configuration-driven capability that lets email templates use **live data from external systems at render time** without exposing external URLs, credentials, raw API responses, or partner-specific response paths inside the template.

Administrators configure and publish reusable external data integrations. Marketers consume only the approved and normalized fields exposed by those integrations.

**Status:** Proof of Concept — active development.

---

## Table of contents

* [Motivation](#motivation)
* [Core concept](#core-concept)
* [User roles](#user-roles)
* [How it works](#how-it-works)
* [Architecture](#architecture)
* [cURL import](#curl-import)
* [Response parsing and field discovery](#response-parsing-and-field-discovery)
* [Field mapping and normalization](#field-mapping-and-normalization)
* [Data storage](#data-storage)
* [Marketer experience](#marketer-experience)
* [Runtime resolution](#runtime-resolution)
* [Caching and fallback](#caching-and-fallback)
* [Database model](#database-model)
* [API](#api)
* [Project structure](#project-structure)
* [Security](#security)
* [Design decisions](#design-decisions)
* [Technology stack](#technology-stack)

---

## Motivation

Email campaigns often need data that remains current until the message is rendered, such as:

* Product name
* Product image
* Current price
* Available stock
* Loyalty balance
* Booking status
* Delivery information
* Personalized recommendations

A common approach is to place an external URL directly inside the email template:

```liquid
{% connected_content
   https://api.partner.com/products/{{ event.product_id }}
   :save product
%}

{{ product.inventory.available }}
```

Although flexible, this approach introduces significant problems in a multi-tenant enterprise platform:

* **SSRF risk:** A template author may access internal services, private IP addresses, or cloud metadata endpoints.
* **Secret leakage:** API keys and authentication tokens may appear inside templates.
* **Partner-schema leakage:** Templates become dependent on raw paths such as `product.inventory.available`.
* **Data exposure:** The entire external response may become available to the rendering layer.
* **No centralized governance:** Timeout, retry, caching, authentication, and fallback rules are distributed across templates.
* **Difficult change management:** A partner API field rename may break every template that uses it.
* **Limited cross-channel reuse:** Email, SMS, push, and web implementations may each build separate integrations.

The Content Pipeline solves this by separating external data access from message presentation.

---

## Core concept

Templates do not call arbitrary URLs.

Instead, the platform stores a reusable, versioned integration containing:

* An **upstream configuration** describing how to call the external source
* A **transformer configuration** describing which response fields to extract and how to rename them
* An **output schema** describing the normalized content contract
* A **resolution policy** describing when and how data should be refreshed
* A **renderer configuration** describing how the normalized object is exposed to Liquid

The template consumes only stable variables:

```liquid
{{ contentLayer.externalProduct.productName }}
{{ contentLayer.externalProduct.currentPrice }}
{{ contentLayer.externalProduct.availableStock }}
```

The platform owns the integration. The template owns only presentation.

---

## User roles

### Administrator or technical user

The administrator:

* Imports or manually configures an external request
* Reviews URL, method, headers, query parameters, and authentication
* Marks request values as static or dynamic
* Tests the external source
* Reviews the parsed response
* Selects approved response fields
* Gives selected fields stable output names
* Configures types, defaults, freshness, and fallback behavior
* Publishes an immutable integration version

### Marketer

The marketer:

* Selects a published external data integration
* Binds approved input parameters to profile, event, campaign, or static values
* Chooses among approved output fields
* Inserts normalized Liquid variables
* Never sees the source URL, credentials, request headers, or raw response paths

---

## How it works

### Configuration flow

```text
Administrator imports cURL
        ↓
Platform parses cURL into request configuration
        ↓
Sensitive values are moved to AWS Secrets Manager
        ↓
Request configuration is stored as a draft
        ↓
Administrator marks dynamic request parameters
        ↓
Platform safely tests the external endpoint
        ↓
JSON response is parsed into an internal object
        ↓
Response schema and field tree are discovered
        ↓
Administrator selects and renames approved fields
        ↓
Platform generates field mappings and output schema
        ↓
Administrator publishes an immutable pipeline version
```

### Marketer flow

```text
Marketer opens the email editor
        ↓
Selects a published external data integration
        ↓
Binds required inputs such as productId
        ↓
Selects approved output fields
        ↓
Platform stores a structured campaign binding
        ↓
Normalized values are resolved before Liquid rendering
```

### Runtime flow

```text
Email render request
        ↓
Load campaign integration bindings
        ↓
Resolve approved input parameters
        ↓
Call Dynamic Content Resolver
        ↓
Check Redis for a fresh normalized result
        ↓
Fetch partner API when necessary
        ↓
Parse the JSON response
        ↓
Apply stored field mappings
        ↓
Create a new normalized object
        ↓
Validate the object against JSON Schema
        ↓
Cache normalized output
        ↓
Inject normalized output into Liquid
        ↓
Render the email
```

---

## Architecture

The pipeline contains three configurable layers supported by dedicated runtime components.

| Component                    | Responsibility                                                                   | Stored configuration                                                                |
| ---------------------------- | -------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| **Upstream**                 | Describes where and how external data is retrieved                               | URL template, method, parameters, headers, authentication reference, timeout, retry |
| **Parser**                   | Converts the bounded HTTP response into an internal JSON object                  | Expected response format and parsing limits                                         |
| **Transformer / Normalizer** | Extracts approved fields and creates a new normalized object                     | JMESPath expressions, output names, types, defaults, required fields                |
| **Output validator**         | Verifies that normalized data satisfies the published contract                   | JSON Schema                                                                         |
| **Resolver**                 | Coordinates caching, refresh, fetching, transformation, validation, and fallback | Resolution scope, freshness window, timeout, cache, fallback                        |
| **Renderer adapter**         | Exposes normalized data to the message renderer                                  | Root namespace and Liquid namespace                                                 |

```text
Partner API
    ↓
Upstream Request Builder
    ↓
Secure Fetcher
    ↓
JSON Parser
    ↓
Normalization Engine
    ↓
JSON Schema Validator
    ↓
Dynamic Content Resolver
    ↓
Liquid Context Adapter
    ↓
Email Renderer
```

---

## cURL import

Administrators can configure an integration using **Import from cURL**.

Example:

```bash
curl --request GET \
  'https://api.partner.com/products/P123?locale=en-US' \
  --header 'Authorization: Bearer secret-token-123' \
  --header 'Accept: application/json'
```

The backend parses the command into a canonical request model:

```json
{
  "method": "GET",
  "baseUrl": "https://api.partner.com",
  "path": "/products/P123",
  "queryParameters": [
    {
      "name": "locale",
      "value": "en-US"
    }
  ],
  "headers": [
    {
      "name": "Accept",
      "value": "application/json",
      "sensitive": false
    },
    {
      "name": "Authorization",
      "value": "Bearer secret-token-123",
      "sensitive": true
    }
  ]
}
```

### The cURL command is parsed, not executed

The platform must never execute the imported command through a shell:

```go
// Never do this.
exec.Command("sh", "-c", curlText)
```

The cURL string is treated as untrusted text.

The importer:

1. Tokenizes the command
2. Reads a supported subset of cURL flags
3. Extracts the URL, method, headers, query parameters, authentication, and body
4. Rejects unsupported or dangerous options
5. Converts the request into platform-owned configuration

The MVP may support:

* `-X` and `--request`
* `-H` and `--header`
* `-u` and `--user`
* `--url`
* `-G` and `--get`
* `-d`, `--data`, and `--data-raw` for controlled JSON requests

Options involving local files, proxies, shell expansion, custom DNS resolution, certificates, or command execution must be rejected.

---

## Sensitive values

Sensitive values are stored separately from the pipeline configuration.

For example, the imported cURL may contain:

```text
Authorization: Bearer secret-token-123
```

The backend extracts the token and stores it in AWS Secrets Manager:

```text
Secret name:
tenant-45/content-pipelines/draft-789/bearer-token

Secret value:
secret-token-123
```

PostgreSQL stores only the secret reference:

```json
{
  "authentication": {
    "type": "bearer_token",
    "secretReference": "tenant-45/content-pipelines/draft-789/bearer-token"
  }
}
```

The frontend receives only masked metadata:

```json
{
  "authentication": {
    "type": "bearer_token",
    "configured": true,
    "maskedValue": "••••••••••••"
  }
}
```

The original secret is not returned to the browser after it has been stored.

The original cURL string is not stored by default because it may contain credentials. A redacted representation may be retained for auditing.

---

## Dynamic request parameters

The importer can identify values in:

* URL paths
* Query parameters
* Headers
* Request bodies

However, it cannot reliably determine whether an imported value is static or dynamic.

For example:

```text
/products/P123
```

`P123` may be a fixed value or a sample product identifier.

The administrator therefore marks the value as dynamic:

```text
Imported value: P123
Parameter name: productId
Location: path
Type: string
Required: true
Example value: P123
```

The URL becomes:

```text
https://api.partner.com/products/{productId}
```

The parameter contract is stored as configuration:

```json
{
  "parameters": {
    "productId": {
      "location": "path",
      "type": "string",
      "required": true,
      "exampleValue": "P123",
      "validation": {
        "pattern": "^[A-Za-z0-9_-]+$",
        "maximumLength": 100
      }
    }
  }
}
```

Only explicitly defined parameters may be supplied during message rendering.

A marketer cannot provide an arbitrary hostname, path, header, or URL.

---

## Response parsing and field discovery

When the administrator selects **Test Connection**, the platform:

1. Loads the draft upstream configuration
2. Resolves example parameter values
3. Retrieves credentials from AWS Secrets Manager
4. Builds the request using Go’s HTTP client
5. Applies SSRF and network security controls
6. Calls the external endpoint
7. Enforces response-size and timeout limits
8. Verifies the content type
9. Parses the JSON response
10. Builds a field tree and inferred schema

Example partner response:

```json
{
  "product": {
    "name": "Acme Motor",
    "inventory": {
      "available": 121,
      "reserved": 20
    },
    "pricing": {
      "current": 249.99
    },
    "supplier": {
      "email": "private@example.com"
    }
  }
}
```

The backend temporarily parses it into an internal Go object:

```go
map[string]any
```

The complete raw response exists only in bounded memory during processing.

### Field discovery

The schema discovery service recursively collects:

* Field name
* Source path
* JMESPath expression
* Detected type
* Sample value
* Nullable status
* Array information
* Child fields
* Whether the field can be selected

Example node:

```json
{
  "name": "available",
  "jsonPath": "$.product.inventory.available",
  "jmesPath": "product.inventory.available",
  "type": "integer",
  "selectable": true,
  "sampleValue": 121
}
```

The frontend renders the response as a clickable tree:

```text
▼ product
   ├── name                         string    [Select]
   ▼ inventory
       ├── available                integer   [Select]
       └── reserved                 integer   [Select]
   ▼ pricing
       └── current                  number    [Select]
   ▼ supplier
       └── email                    string    [Select]
```

---

## Field mapping and normalization

The administrator chooses which response fields may be exposed.

For example:

| Source path                   | Output name      | Display label   | Type    |
| ----------------------------- | ---------------- | --------------- | ------- |
| `product.name`                | `productName`    | Product name    | string  |
| `product.inventory.available` | `availableStock` | Available stock | integer |
| `product.pricing.current`     | `currentPrice`   | Current price   | number  |

The administrator does not choose values such as `"Acme Motor"` or `121` to be permanently copied.

The administrator chooses the **mapping instructions** that will be evaluated whenever the integration runs.

The backend stores:

```json
{
  "type": "jmespath_mapping",
  "fields": {
    "productName": {
      "sourcePath": "product.name",
      "type": "string",
      "required": true
    },
    "availableStock": {
      "sourcePath": "product.inventory.available",
      "type": "integer",
      "required": false,
      "default": 0
    },
    "currentPrice": {
      "sourcePath": "product.pricing.current",
      "type": "number",
      "required": true
    }
  }
}
```

This configuration means:

```text
Read product.name
→ place its value in productName

Read product.inventory.available
→ place its value in availableStock

Read product.pricing.current
→ place its value in currentPrice
```

At runtime, the transformer evaluates the stored paths:

```go
for outputName, mapping := range config.Fields {
    value, err := jmespath.Search(mapping.SourcePath, rawResponse)
    if err != nil {
        return err
    }

    normalized[outputName] = value
}
```

The platform constructs a completely new object:

```json
{
  "productName": "Acme Motor",
  "availableStock": 121,
  "currentPrice": 249.99
}
```

The raw response is not modified and forwarded.

Only the selected fields are copied into the new normalized object.

The following unselected fields are discarded:

```json
{
  "reserved": 20,
  "supplier": {
    "email": "private@example.com"
  }
}
```

### Output schema

The platform generates a JSON Schema from the selected mappings:

```json
{
  "type": "object",
  "required": [
    "productName",
    "currentPrice"
  ],
  "properties": {
    "productName": {
      "type": "string"
    },
    "availableStock": {
      "type": "integer",
      "minimum": 0,
      "default": 0
    },
    "currentPrice": {
      "type": "number",
      "minimum": 0
    }
  },
  "additionalProperties": false
}
```

The mapping defines where values come from.

The output schema defines what the normalized result must look like.

---

## Data storage

The system stores configuration, secrets, normalized outputs, and temporary raw data in different locations.

### PostgreSQL

PostgreSQL permanently stores:

* Pipeline identity
* Tenant ownership
* Draft and published pipeline versions
* Upstream request configuration
* Secret references
* Dynamic parameter definitions
* Field mapping instructions
* Friendly output names
* Output JSON Schema
* Renderer namespace
* Resolution policy
* Cache policy
* Fallback policy
* Last-known-good normalized outputs
* Campaign integration bindings
* Execution metadata

PostgreSQL does not normally store:

* Authentication tokens
* Passwords
* Complete raw partner responses
* Arbitrary cURL commands containing credentials

### AWS Secrets Manager

AWS Secrets Manager stores:

* API keys
* Bearer tokens
* Basic-auth passwords
* OAuth client secrets
* OAuth refresh tokens

PostgreSQL stores only references to these values.

### Redis

Redis temporarily stores:

* Current normalized output
* Fetch timestamp
* Freshness deadline
* Maximum stale deadline
* Pipeline version
* Distributed refresh locks

Example:

```json
{
  "output": {
    "productName": "Acme Motor",
    "availableStock": 121,
    "currentPrice": 249.99
  },
  "metadata": {
    "pipelineVersion": 4,
    "fetchedAt": "2026-07-29T07:45:00Z",
    "freshUntil": "2026-07-29T07:45:20Z",
    "maximumStaleUntil": "2026-07-29T08:45:00Z"
  }
}
```

### Temporary memory

The complete raw response exists temporarily while the platform:

```text
Fetches
→ parses
→ maps
→ validates
```

After normalization, the raw response is discarded.

Sanitized response samples may be stored temporarily for debugging with:

* Short retention
* Encryption
* Response-size limits
* PII redaction
* Audited access

### Storage summary

```text
PostgreSQL
→ stores the recipe

AWS Secrets Manager
→ stores sensitive credentials

Temporary memory
→ holds the full raw response during execution

Redis
→ stores the current normalized result

PostgreSQL last-known-good table
→ stores the latest usable normalized result
```

---

## Marketer experience

After an integration is published, the marketer opens:

```text
Personalization
→ External Data
→ Product Information API
```

The editor displays:

```text
Required inputs
  Product ID

Available fields
  Product name
  Available stock
  Current price
```

### Input binding

The marketer binds the approved `productId` input:

```text
productId
=
Event field → product_id
```

The campaign stores a structured binding:

```json
{
  "pipelineId": "pipeline-123",
  "namespace": "externalProduct",
  "inputBindings": {
    "productId": {
      "source": "event",
      "path": "product_id"
    }
  }
}
```

The campaign does not store:

* The external URL
* Authentication credentials
* Request headers
* Raw response paths

### Field insertion

Selecting approved fields inserts:

```liquid
{{ contentLayer.externalProduct.productName }}
{{ contentLayer.externalProduct.currentPrice }}
{{ contentLayer.externalProduct.availableStock }}
```

The marketer never sees:

```text
product.inventory.available
```

The marketer sees:

```text
Available stock
```

---

## Runtime resolution

The campaign binding is resolved before Liquid starts rendering.

Suppose the event contains:

```json
{
  "product_id": "P123"
}
```

The renderer:

1. Loads the campaign binding
2. Reads `event.product_id`
3. Creates the approved input `productId=P123`
4. Calls the Dynamic Content Resolver
5. Receives the normalized object
6. Injects it into the Liquid context
7. Renders the message

Resolver request:

```http
POST /internal/v1/content-pipelines/pipeline-123:resolve
```

```json
{
  "tenantId": "tenant-45",
  "parameters": {
    "productId": "P123"
  }
}
```

The resolver performs:

```text
Load active pipeline version
        ↓
Validate tenant ownership
        ↓
Validate input parameters
        ↓
Calculate parameter hash
        ↓
Check Redis
        ↓
Use fresh value or acquire refresh lock
        ↓
Retrieve secret from AWS Secrets Manager
        ↓
Build the approved HTTP request
        ↓
Apply SSRF controls
        ↓
Fetch partner response
        ↓
Parse JSON
        ↓
Evaluate stored mappings
        ↓
Construct normalized output
        ↓
Validate against JSON Schema
        ↓
Cache normalized output
        ↓
Update last-known-good output
        ↓
Return normalized data
```

The Liquid context becomes:

```json
{
  "contentLayer": {
    "externalProduct": {
      "productName": "Acme Motor",
      "availableStock": 121,
      "currentPrice": 249.99
    }
  }
}
```

---

## Caching and fallback

Freshness is evaluated when the data is needed.

A resolution policy may contain:

```json
{
  "trigger": "message_render",
  "scope": "per_unique_parameter",
  "keyParameters": [
    "productId"
  ],
  "freshnessWindowSeconds": 20,
  "timeoutMs": 1500
}
```

A cache key contains:

* Tenant ID
* Pipeline ID
* Pipeline version
* Hash of normalized input parameters

Example:

```text
cl:tenant-45:pipeline-123:v4:4bb8f934...
```

If many messages request the same product simultaneously, a distributed refresh lock ensures that only one worker calls the partner API.

Fallback order:

```text
Fresh Redis value
→ stale value within allowed limit
→ PostgreSQL last-known-good output
→ configured default output
→ hide the content block
→ abort the message for critical integrations
```

---

## Database model

### `content_pipelines`

Stores the stable identity of each integration.

```sql
CREATE TABLE content_pipelines (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    active_version_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
```

### `content_pipeline_versions`

Stores draft and immutable published configurations.

```sql
CREATE TABLE content_pipeline_versions (
    id UUID PRIMARY KEY,
    pipeline_id UUID NOT NULL,
    version_number INTEGER NOT NULL,
    upstream_config JSONB NOT NULL,
    transformer_config JSONB NOT NULL,
    output_schema JSONB NOT NULL,
    renderer_config JSONB NOT NULL,
    execution_config JSONB NOT NULL,
    fallback_config JSONB NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (pipeline_id, version_number)
);
```

Example configuration:

```json
{
  "upstreamConfig": {
    "method": "GET",
    "urlTemplate": "https://api.partner.com/products/{productId}",
    "authentication": {
      "type": "bearer_token",
      "secretReference": "tenant-45/product-api-token"
    },
    "parameters": {
      "productId": {
        "location": "path",
        "type": "string",
        "required": true
      }
    }
  },
  "transformerConfig": {
    "type": "jmespath_mapping",
    "fields": {
      "productName": {
        "sourcePath": "product.name",
        "type": "string"
      },
      "availableStock": {
        "sourcePath": "product.inventory.available",
        "type": "integer"
      }
    }
  },
  "rendererConfig": {
    "rootNamespace": "contentLayer",
    "namespace": "externalProduct"
  }
}
```

### `content_pipeline_outputs`

Stores last-known-good normalized output.

```sql
CREATE TABLE content_pipeline_outputs (
    tenant_id UUID NOT NULL,
    pipeline_version_id UUID NOT NULL,
    parameter_hash VARCHAR(64) NOT NULL,
    normalized_output JSONB NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    maximum_stale_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (
        tenant_id,
        pipeline_version_id,
        parameter_hash
    )
);
```

### `campaign_external_data_bindings`

Stores campaign references to published integrations.

```sql
CREATE TABLE campaign_external_data_bindings (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    campaign_id UUID NOT NULL,
    pipeline_id UUID NOT NULL,
    namespace VARCHAR(255) NOT NULL,
    input_bindings JSONB NOT NULL,
    selected_fields JSONB,
    created_at TIMESTAMPTZ NOT NULL
);
```

### `content_pipeline_test_samples`

Optionally stores temporary sanitized samples.

```sql
CREATE TABLE content_pipeline_test_samples (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    pipeline_id UUID NOT NULL,
    sanitized_response JSONB NOT NULL,
    discovered_schema JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
```

---

## API

### Integration configuration

```http
POST /v1/content-pipelines
POST /v1/content-pipelines/import-curl
PUT  /v1/content-pipelines/{id}/draft
POST /v1/content-pipelines/{id}/test-source
POST /v1/content-pipelines/{id}/discover-schema
PUT  /v1/content-pipelines/{id}/field-mappings
POST /v1/content-pipelines/{id}/preview-transformation
POST /v1/content-pipelines/{id}/validate
POST /v1/content-pipelines/{id}/publish
POST /v1/content-pipelines/{id}/rollback
```

### Marketer editor

```http
GET /v1/editor/external-data-sources
GET /v1/editor/external-data-sources/{id}
PUT /v1/campaigns/{campaignId}/external-data-bindings
```

### Runtime resolution

```http
POST /internal/v1/content-pipelines/{id}:resolve
```

---

## Project structure

```text
cmd/
  api/
  worker/

internal/
  curlimport/
    tokenizer.go
    parser.go
    validator.go

  pipeline/
    model.go
    service.go
    publisher.go
    repository.go

  upstream/
    request_builder.go
    parameter_validator.go

  security/
    ssrf.go
    dns_validator.go
    redirect_validator.go

  fetcher/
    client.go
    limits.go

  responseparser/
    json_parser.go

  schemainfer/
    infer.go
    node.go

  transformer/
    jmespath.go
    type_conversion.go

  outputvalidation/
    jsonschema.go

  resolver/
    resolver.go
    fallback.go
    locking.go

  cache/
    redis.go
    keys.go

  secrets/
    secrets_manager.go

  renderer/
    liquid_context.go

  execution/
    metrics.go
    repository.go
```

---

## Security

The platform must protect against:

* SSRF
* Localhost and private-network access
* Cloud metadata endpoint access
* DNS rebinding
* Malicious redirects
* Shell command injection
* Secret leakage
* Cross-tenant access
* Excessively large responses
* Slow external endpoints
* Compression bombs
* Invalid content types
* Arbitrary code execution

Required controls include:

* Parse cURL without executing it
* HTTPS by default
* Domain verification or allowlisting
* DNS and resolved-IP validation
* Private and reserved IP blocking
* Redirect revalidation
* Request timeouts
* Response-size limits
* Restricted HTTP methods
* Restricted cURL options
* Secret references instead of plaintext credentials
* Parameter schemas and validation
* Per-tenant rate limits
* Per-domain concurrency limits
* Output JSON Schema validation
* Audit logging
* Tenant isolation

---

## Design decisions

### Store mappings, not raw values

During integration configuration, the database stores:

```text
productName → product.name
availableStock → product.inventory.available
```

It does not normally store:

```text
productName = "Acme Motor"
availableStock = 121
```

The values are read when the integration runs.

### Store secrets separately

Credentials are stored in AWS Secrets Manager.

PostgreSQL stores only secret references.

### Build a new normalized object

The transformer does not forward the complete response.

It constructs a new object containing only approved fields.

### Keep templates presentation-only

Campaigns store structured integration bindings outside Liquid.

Liquid receives an already-resolved content context.

### Publish immutable versions

Draft integrations may change.

Published versions are immutable and may be rolled back safely.

### Preserve stable template fields

If a partner changes:

```text
product.inventory.available
```

to:

```text
product.stock.currentQuantity
```

the administrator updates the stored mapping:

```json
{
  "availableStock": {
    "sourcePath": "product.stock.currentQuantity"
  }
}
```

The marketer’s template remains unchanged:

```liquid
{{ contentLayer.externalProduct.availableStock }}
```

### Cache normalized output only

Redis and last-known-good storage contain the safe normalized object, not the complete partner response.

---

## Technology stack

* **Go:** APIs, cURL importer, request builder, secure fetcher, parser, schema discovery, resolver, and workers
* **PostgreSQL with JSONB:** Pipeline configuration, mappings, schemas, versions, campaign bindings, and last-known-good outputs
* **Redis:** Normalized output caching and distributed refresh locks
* **AWS Secrets Manager:** External API credentials
* **JMESPath:** Field extraction and normalization
* **JSON Schema:** Output-contract validation
* **Liquid:** Email presentation
* **Amazon SQS:** Asynchronous refresh jobs
* **Amazon EventBridge Scheduler:** Scheduled refreshes
* **Liquibase:** PostgreSQL schema migrations

---

## Summary

The Content Pipeline treats external data integrations as versioned configuration rather than template code.

```text
cURL
→ safe upstream configuration
→ external response
→ temporary parsed JSON
→ admin-approved mappings
→ normalized content object
→ Redis and last-known-good storage
→ structured campaign binding
→ Liquid context
→ rendered message
```

The database stores the integration recipe.

AWS Secrets Manager stores credentials.

The raw response is processed temporarily.

Only selected normalized values are exposed to marketers and message templates.
 