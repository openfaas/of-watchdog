# of-watchdog

Reverse proxy for HTTP microservices and STDIO

[![Go Report Card](https://goreportcard.com/badge/github.com/openfaas/of-watchdog)](https://goreportcard.com/report/github.com/openfaas/of-watchdog) [![Build Status](https://travis-ci.org/openfaas/of-watchdog.svg?branch=master)](https://travis-ci.org/openfaas/of-watchdog)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![OpenFaaS](https://img.shields.io/badge/openfaas-serverless-blue.svg)](https://www.openfaas.com)

The `of-watchdog` implements an HTTP server listening on port 8080, and acts as a reverse proxy for running functions and microservices. It can be used independently, or as the entrypoint for a container with OpenFaaS.

This version of the OpenFaaS watchdog adds support for HTTP proxying as well as STDIO, which enables reuse of memory and very fast serving of requests. It does not aim to replace the [Classic Watchdog](https://github.com/openfaas/classic-watchdog), but offers another option for those who need these features.

### Goals:

- Keep function process warm for lower latency / caching / persistent connections through using HTTP
- Enable streaming of large responses from functions, beyond the RAM or disk capacity of the container
- Cleaner abstractions for each "mode"

## Modes

There are several modes available for the of-watchdog which changes how it interacts with your microservice or function code.

![](https://docs.openfaas.com/architecture/watchdog-modes.png)

> A comparison of three watchdog modes. Top left - Classic Watchdog, top right: afterburn (deprecated), bottom left HTTP mode from of-watchdog.

1. HTTP mode - the default and most efficient option all template authors should consider this option if the target language has a HTTP server implementation.
2. Serializing mode - for when a HTTP server implementation doesn't exist, STDIO is read into memory then sent into a forked process.
3. Streaming mode - as per serializing mode, however the request and response are both streamed instead of being buffered completely into memory before the function starts running.

### API

Private endpoints, served by watchdog:

- `/_/health` - returns true when the process is started, or if a lock file is in use, when that file exists.
- `/_/ready` - as per `/_/health`, but if `max_inflight` is configured to a non-zero value, and the maximum number of connections is met, it will return a 429 status

Any other HTTP requests:

- `/*` any other Path and HTTP verbs are sent to the function

### 1. HTTP (mode=http)

#### 1.1 Status

HTTP mode is recommend for all templates where the target language has a HTTP server implementation available.

See a few different examples of templates, more are available via `faas-cli template store list`

| Template         | HTTP framework     | Repo                                              |
| ---------------- | ------------------ | ------------------------------------------------- |
| Node.js 12 (LTS) | Express.js         | https://github.com/openfaas/templates/            |
| Python 3 & 2.7   | Flask              | https://github.com/openfaas/python-flask-template |
| Golang           | Go HTTP (stdlib)   | https://github.com/openfaas/golang-http-template  |
| Golang           | (http.HandlerFunc) | https://github.com/openfaas/golang-http-template  |
| Ruby             | Sinatra            | https://github.com/openfaas/ruby-http             |
| Java 11          | Sun HTTP / Gradle  | https://github.com/openfaas/templates/            |

Unofficial: [.NET Core / C# and Kestrel](https://github.com/burtonr/csharp-kestrel-template)

#### 1.2 Description

A process is forked when the watchdog starts, we then forward any request incoming to the watchdog to a HTTP port within
the container.

Pros:

- Fastest option for high concurrency and throughput
- More efficient concurrency and RAM usage vs. forking model
- Database connections can be persisted for the lifetime of the container
- Files or models can be fetched and stored in `/tmp/` as a one-off initialization task and used for all requests after that
- Does not require new/custom client libraries like afterburn but makes use of a long-running daemon such as Express.js for Node or Flask for Python

Example usage for testing:

- Forward to an Nginx container:

```
$ go build ; mode=http port=8081 fprocess="docker run -p 80:80 --name nginx -t nginx" upstream_url=http://127.0.0.1:80 ./of-watchdog
```

- Forward to a Node.js / Express.js hello-world app:

```
$ go build ; mode=http port=8081 fprocess="node expressjs-hello-world.js" upstream_url=http://127.0.0.1:3000 ./of-watchdog
```

Cons:

- One more HTTP hop in the chain between the client and the function
- Daemons such as express/flask/sinatra can be unpredictable when used in this way so many need additional configuration
- Additional memory may be occupied between invocations vs. forking model

### 2. Serializing fork (mode=serializing)

#### 2.1 Status

This mode is designed to replicate the behaviour of the original watchdog for backwards compatibility.

#### 2.2 Description

Forks one process per request. Multi-threaded. Ideal for retro-fitting a CGI application handler i.e. for Flask.

![](https://camo.githubusercontent.com/61c169ab5cd01346bc3dc7a11edc1d218f0be3b4/68747470733a2f2f7062732e7477696d672e636f6d2f6d656469612f4447536344626c554941416f34482d2e6a70673a6c61726765)

Limited to processing files sized as per available memory.

Reads entire request into memory from the HTTP request. At this point we serialize or modify if required. That is then written into the stdin pipe.

- Stdout pipe is read into memory and then serialized or modified if necessary before being written back to the HTTP response.
- A static Content-type can be set ahead of time.
- HTTP headers can be set even after executing the function (not implemented).
- Exec timeout: supported.

### 3. Streaming fork (mode=streaming) - default.

Forks a process per request and can deal with a request body larger than memory capacity - i.e. 512mb VM can process multiple GB of video.

HTTP headers cannot be sent after function starts executing due to input/output being hooked-up directly to response for
streaming efficiencies. Response code is always 200 unless there is an issue forking the process. An error mid-flight
will have to be picked up on the client. Multi-threaded.

- Input is sent back to client as soon as it's printed to stdout by the executing process.
- A static Content-type can be set ahead of time.
- Exec timeout: supported.

### 4. Static (mode=static)

This mode starts an HTTP file server for serving static content found at the directory specified by `static_path`.

See an example in the [Hugo blog post](https://www.openfaas.com/blog/serverless-static-sites/).

## Auth

The watchdog has an _OPTIONAL_ auth middleware that leverages [Open Policy Agent](https://www.openpolicyagent.org/) to allow maximum flexibility.

This allows writing authentication and authorization policies in Rego and applying these before the invocation is sent to your function implementation. This decouples the auth layer from your function layer, allowing your to write the authentication logic once and then easily reuse it across your functions.

The auth policy can be loaded from a secret _or_ can be built directly into your function.

### Loading auth policy from a secret
If you have created an auth policy as a secret, then you simply need to add the secret to your `secrets` and then set the `opa_policy` environment variable to the secret name during deployment. You must also set the `opa_query` environment variable.

For example, if you have a policy called `basic_auth_policy` then you would

1. create a secret named `basic_auth_policy`
2. assign / require this secret in your function spec
3. and set the following environment variable:

	```env
	opa_policy=basic_auth_policy
	```

### Example basic auth

You can implement a simplified basic auth with the following policy as the secret `basic_auth_policy`

```rego
package api.auth.basic

default allow = false

allow {
	valid_basic_auth
}

valid_basic_auth {
	value := trim_prefix(input.authorization, "Basic ")
	decoded := base64.decode(value)

	idx := indexof(decoded, ":")
	username := substring(decoded, 0, idx)
	password := substring(decoded, idx + 1, -1)

	credentials[username] == password
}

credentials := {"bob": "secretvalue"}
```

Then your function would need the environment variables

```env
opa_policy=basic_auth_policy
opa_query=data.api.auth.basic
```

Note: the OPA query will always be `data.<package name>`

### Policy Input

The policy input will receive the following input data:

| key                   | description                                                                                            |
| --------------------- | ------------------------------------------------------------------------------------------------------ |
| `input.method`        | the request method, e.g. `POST` or `GET`                                                               |
| `input.path`          | the request path, e.g. `/api/profile`                                                                  |
| `input.authorization` | the `Authorization` header value                                                                       |
| `input.headers`       | the full request headers, note this is a map from string to a list of string, ie `map[string][]string` |
| `input.rawBody`       | the request body as a string                                                                           |
| `input.body`          | the JSON parsed request body                                                                           |
| `input.data`          | additional input data as configured via the `opa_input_*` environnement variables                      |

The `headers`, `rawBody`, and `body` are not included by default. Each is controlled the value of the `opa_include_headers`, `opa_include_raw_body`, and `opa_include_body` environment variables respectively.

The `data` field contains additional data from the environment variables that have the prefix `opa_input`. For example, `opa_input_valid_issuers=http://google.com` will then be available to the policy as `input.data.valid_issuers`.

Secret values can also be loaded into the `data` field, this is controlled via the `opa_input_secrets`. This can be a comma separated list of secrets to be loaded. For example, if you have a secret name `slack_hash_key` and you set `opa_input_secrets=slack_hash_key`, you can access the value from `input.data.slack_hash_key`

### Policy output
The middleware supports two types of policy output: simple and structured.

#### Simple boolean output
The simplest form of policy output is a boolean value. If the policy returns `true` then the request will be allowed to proceed, if the policy returns `false` then the request will be rejected with a `401` status code.

Examples can be found in the [basic auth policy](/auth/testdata/basic_auth.rego) and the [HMAC policy](/auth/testdata/hmac_auth.rego)

#### Structured response output
The policy can also return a structured response. This allows the policy to return additional context to the handler by specifying additional headers to add to the request. The response must be a JSON object with the following fields:

| Key         | Description                                                                 |
| ----------- | --------------------------------------------------------------------------- |
| `allow`     | a boolean value indicating if the request should be allowed or not          |
| `status`    | (optional) the status code to return if the request is **rejected**. Defaults to `401` |
| `headers`   | (optional) a map of headers to be added to the request before it is sent to your implementation. It is only used if the request **is allowed**.  |

The primary use case would be extracting client or user information from the request and then adding it to the request headers (for example, as `X-User-Email`) so that your implementation can use this information to perform additional actions or enable auditing.

## Metrics

| Name                          | Description                  | Type      |
| ----------------------------- | ---------------------------- | --------- |
| http_requests_total           | Total number of requests     | Counter   |
| http_request_duration_seconds | Duration of requests         | Histogram |
| http_requests_in_flight       | Number of requests in-flight | Gauge     |

## Configuration

Environmental variables:

> Note: timeouts should be specified as Golang durations i.e. `1m` or `20s`.

| Option                          | Usage                                                                                                                                                                                                                                                                                                        |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `fprocess` / `function_process` | Process to execute a server in `http` mode or to be executed for each request in the other modes. For non `http` mode the process must accept input via STDIN and print output via STDOUT. Also known as "function process".                                                                                 |
| `mode`                          | The mode which of-watchdog operates in, Default `streaming` [see doc](#3-streaming-fork-modestreaming---default). Options are [http](#1-http-modehttp), [serialising fork](#2-serializing-fork-modeserializing), [streaming fork](#3-streaming-fork-modestreaming---default), [static](#4-static-modestatic) |
| `read_timeout`                  | HTTP timeout for reading the payload from the client caller (in seconds)                                                                                                                                                                                                                                     |
| `write_timeout`                 | HTTP timeout for writing a response body from your function (in seconds)                                                                                                                                                                                                                                     |
| `exec_timeout`                  | Exec timeout for process exec'd for each incoming request (in seconds). Disabled if set to 0.                                                                                                                                                                                                                |
| `max_inflight`                  | Limit the maximum number of requests in flight, and return a HTTP status 429 when exceeded                                                                                                                                                                                                                   |
| `prefix_logs`                   | When set to `true` the watchdog will add a prefix of "Date Time" + "stderr/stdout" to every line read from the function process. Default `true`                                                                                                                                                              |
| `log_buffer_size`               | The amount of bytes to read from stderr/stdout for log lines. When exceeded, the user will see an "bufio.Scanner: token too long" error. The default value is `bufio.MaxScanTokenSize`                                                                                                                       |
| `healthcheck_interval`          | Interval (in seconds) for HTTP healthcheck by container orchestrator i.e. kubelet. Used for graceful shutdowns.                                                                                                                                                                                              |
| `port`                          | Specify an alternative TCP port for testing. Default: `8080`                                                                                                                                                                                                                                                 |
| `content_type`                  | Force a specific Content-Type response for all responses - only in forking/serializing modes.                                                                                                                                                                                                                |
| `suppress_lock`                 | When set to `false` the watchdog will attempt to write a lockfile to `/tmp/.lock` for healthchecks. Default `false`                                                                                                                                                                                          |
| `http_upstream_url`             | `http` mode only - where to forward requests i.e. `127.0.0.1:5000`                                                                                                                                                                                                                                           |
| `upstream_url`                  | alias for `http_upstream_url`                                                                                                                                                                                                                                                                                |
| `http_buffer_req_body`          | `http` mode only - buffers request body in memory before forwarding upstream to your template's `upstream_url`. Use if your upstream HTTP server does not accept `Transfer-Encoding: chunked`, for example WSGI tends to require this setting. Default: `false`                                              |
| `buffer_http`                   | deprecated alias for `http_buffer_req_body`, will be removed in future version                                                                                                                                                                                                                               |
| `static_path`                   | Absolute or relative path to the directory that will be served if `mode="static"`                                                                                                                                                                                                                            |
| `ready_path`                    | When non-empty, requests to `/_/ready` will invoke the function handler with this path. This can be used to provide custom readiness logic. When `max_inflight` is set, the concurrency limit is checked first before proxying the request to the function.                                                  |
| `opa_policy` | The name of the secret containing the OPA policy to be used. Alternatively, can be a path to a file, e.g. a `tar.gz`, `.rego`, or `wasm` file bundled with the function at build time. |
| `opa_query` | The dot separated query used to evaluate the policy. |
| `opa_debug` | When set to `true`, the OPA policy will be evaluated in debug mode, the output is sent to the function stdout. Additional metadata is also printed by the auth middleware. |
| `opa_skip_paths` | comma separated list of paths to skip policy evaluation, these paths will allow _all_ requests. |
| `opa_include_headers` | boolean value to indicate whether to include the request headers in the OPA input. See also [Policy Input](#policy-input). Default: `false` |
| `opa_include_body` | boolean value to indicate whether to include the request body in the OPA input, the body is will be parsed JSON. See also [Policy Input](#policy-input). Default: `false` |
| `opa_include_raw_body` | boolean value to indicate whether to include the raw request body in the OPA input. See also [Policy Input](#policy-input). Default: `false` |
| `opa_input_secrets` | comma separated list of secrets to include in the OPA input. The values are accessible using `input.data[secret_name]` or `input.data.secret_name`, see also [Policy Input](#policy-input). |
| `opa_input_*` | any other environment variable starting with `opa_input_` will be included in the OPA input. The values are accessible using `input.data[env_var_name]` or `input.data.env_var_name`, see also [Policy Input](#policy-input). |
| `opa_error_content_type` | The content type to set when the policy evaluation fails. Default: `text/plain` |

Unsupported options from the [Classic Watchdog](https://github.com/openfaas/classic-watchdog):

| Option            | Usage                                                                                                                                                                  |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `write_debug`     | In the classic watchdog, this prints the response body out to the console                                                                                              |
| `read_debug`      | In the classic watchdog, this prints the request body out to the console                                                                                               |
| `combined_output` | In the classic watchdog, this returns STDOUT and STDERR in the function's HTTP response, when off it only returns STDOUT and prints STDERR to the logs of the watchdog |
