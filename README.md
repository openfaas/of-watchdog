# of-watchdog

Reverse proxy for HTTP microservices and STDIO

[![Go Report Card](https://goreportcard.com/badge/github.com/openfaas/of-watchdog)](https://goreportcard.com/report/github.com/openfaas/of-watchdog) [![Build Status](https://travis-ci.org/openfaas/of-watchdog.svg?branch=master)](https://travis-ci.org/openfaas/of-watchdog)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![OpenFaaS](https://img.shields.io/badge/openfaas-serverless-blue.svg)](https://www.openfaas.com)

The `of-watchdog` implements a HTTP server listening on port 8080, and acts as a reverse proxy for running functions and microservices. It can be used independently, or as the entrypoint for a container with OpenFaaS.

This version of the OpenFaaS watchdog adds support for HTTP proxying as well as STDIO, which enables re-use of memory and very fast serving of requests. It does not aim to replace the [Classic Watchdog](https://github.com/openfaas/faas/tree/master/watchdog), but offers another option for those who need these features.


### Goals:

* Keep function process warm for lower latency / caching / persistent connections through using HTTP
* Enable streaming of large responses from functions, beyond the RAM or disk capacity of the container
* Cleaner abstractions for each "mode"

## Modes

There are several modes available for the of-watchdog which changes how it interacts with your microservice or function code.

![](https://docs.openfaas.com/architecture/watchdog-modes.png)

> A comparison of three watchdog modes. Top left - Classic Watchdog, top right: afterburn (deprecated), bottom left HTTP mode from of-watchdog.

### 1. HTTP (mode=http)

#### 1.1 Status

The HTTP mode is stable.

See example templates:

| Template               | HTTP framework      | Repo                                                               |
|------------------------|---------------------|--------------------------------------------------------------------|
| Node.js 12 (LTS)       | Express.js          | https://github.com/openfaas/templates/      |
| Python 3 & 2.7         | Flask               | https://github.com/openfaas-incubator/python-flask-template        |
| Golang                 | Go HTTP (stdlib)    | https://github.com/openfaas-incubator/golang-http-template         |
| Golang                 | (http.HandlerFunc)  | https://github.com/openfaas-incubator/golang-http-template         |
| Ruby                   | Sinatra             | https://github.com/openfaas-incubator/ruby-http                    |
| Java 11                | Sun HTTP / Gradle   | https://github.com/openfaas/templates/                             |

Unofficial: [.NET Core / C# and Kestrel](https://github.com/burtonr/csharp-kestrel-template)

#### 1.2 Description

A process is forked when the watchdog starts, we then forward any request incoming to the watchdog to a HTTP port within the container.

Pros:

* Fastest option for high concurrency and throughput

* More efficient concurrency and RAM usage vs. forking model

* Database connections can be persisted for the lifetime of the container

* Files or models can be fetched and stored in `/tmp/` as a one-off initialization task and used for all requests after that

* Does not require new/custom client libraries like afterburn but makes use of a long-running daemon such as Express.js for Node or Flask for Python

Example usage for testing:

* Forward to an NGinx container:

```
$ go build ; mode=http port=8081 fprocess="docker run -p 80:80 --name nginx -t nginx" upstream_url=http://127.0.0.1:80 ./of-watchdog
```

* Forward to a Node.js / Express.js hello-world app:

```
$ go build ; mode=http port=8081 fprocess="node expressjs-hello-world.js" upstream_url=http://127.0.0.1:3000 ./of-watchdog
```

Cons:

* One more HTTP hop in the chain between the client and the function

* Daemons such as express/flask/sinatra can be unpredictable when used in this way so many need additional configuration

* Additional memory may be occupied between invocations vs. forking model

### 2. Serializing fork (mode=serializing)

#### 2.1 Status

This mode is designed to replicate the behaviour of the original watchdog for backwards compatibility.

#### 2.2 Description

Forks one process per request. Multi-threaded. Ideal for retro-fitting a CGI application handler i.e. for Flask.

![](https://camo.githubusercontent.com/61c169ab5cd01346bc3dc7a11edc1d218f0be3b4/68747470733a2f2f7062732e7477696d672e636f6d2f6d656469612f4447536344626c554941416f34482d2e6a70673a6c61726765)

Limited to processing files sized as per available memory.

Reads entire request into memory from the HTTP request. At this point we serialize or modify if required. That is then written into the stdin pipe.

* Stdout pipe is read into memory and then serialized or modified if necessary before being written back to the HTTP response.

* A static Content-type can be set ahead of time.

* HTTP headers can be set even after executing the function (not implemented).

* Exec timeout: supported.

### 3. Streaming fork (mode=streaming) - default.

Forks a process per request and can deal with a request body larger than memory capacity - i.e. 512mb VM can process multiple GB of video.

HTTP headers cannot be sent after function starts executing due to input/output being hooked-up directly to response for streaming efficiencies. Response code is always 200 unless there is an issue forking the process. An error mid-flight will have to be picked up on the client. Multi-threaded.

* Input is sent back to client as soon as it's printed to stdout by the executing process.

* A static Content-type can be set ahead of time.

* Exec timeout: supported.

### 4. Static (mode=static)

This mode starts an HTTP file server for serving static content found at the directory specified by `static_path`.

See an example in the [Hugo blog post](https://www.openfaas.com/blog/serverless-static-sites/).

## Metrics

| Name                            | Description             | Type                   |
|---------------------------------|-------------------------|------------------------|
| http_requests_total             | Total number of requests | Counter               |
| http_request_duration_seconds   | Duration of requests    | Histogram              |
| http_requests_in_flight         | Number of requests in-flight | Gauge             |

## Configuration

Environmental variables:

> Note: timeouts should be specified as Golang durations i.e. `1m` or `20s`. 

| Option                      | Implemented  | Usage                         |
|-----------------------------|--------------|-------------------------------|
| `function_process`          | Yes          | Process to execute a server in `http` mode or to be executed for each request in the other modes. For non `http` mode the process must accept input via STDIN and print output via STDOUT. Also known as "function process". Alias: `fprocess` |
| `static_path`               | Yes          | Absolute or relative path to the directory that will be served if `mode="static"` |
| `read_timeout`              | Yes          | HTTP timeout for reading the payload from the client caller (in seconds) |
| `write_timeout`             | Yes          | HTTP timeout for writing a response body from your function (in seconds)  |
| `healthcheck_interval`      | Yes          | Interval (in seconds) for HTTP healthcheck by container orchestrator i.e. kubelet. Used for graceful shutdowns.  |
| `exec_timeout`              | Yes          | Exec timeout for process exec'd for each incoming request (in seconds). Disabled if set to 0. |
| `port`                      | Yes          | Specify an alternative TCP port for testing. Default: `8080` |
| `write_debug`               | No           | Write all output, error messages, and additional information to the logs. Default is `false`. |
| `content_type`              | Yes          | Force a specific Content-Type response for all responses - only in forking/serializing modes. |
| `suppress_lock`             | Yes          | When set to `false` the watchdog will attempt to write a lockfile to /tmp/ for healthchecks. Default `false` |
| `http_upstream_url`         | Yes          | `http` mode only - where to forward requests i.e. `127.0.0.1:5000` |
| `upstream_url`              | Yes          | alias for `http_upstream_url` |
| `http_buffer_req_body`      | Yes          | `http` mode only - buffers request body in memory before forwarding upstream to your template's `upstream_url`. Use if your upstream HTTP server does not accept `Transfer-Encoding: chunked` Default: `false` |
| `buffer_http`               | Yes          | deprecated alias for `http_buffer_req_body`, will be removed in future version  |
| `max_inflight`              | Yes          | Limit the maximum number of requests in flight |
| `mode`                      | Yes          | The mode which of-watchdog operates in, Default `streaming` [see doc](#3-streaming-fork-modestreaming---default). Options are [http](#1-http-modehttp), [serialising fork](#2-serializing-fork-modeserializing), [streaming fork](#3-streaming-fork-modestreaming---default), [static](#4-static-modestatic) |
| `prefix_logs`             | Yes          | When set to `true` the watchdog will add a prefix of "Date Time" + "stderr/stdout" to every line read from the function process. Default `true` |
| `log_buffer_size` | The amount of bytes to read from stderr/stdout for log lines. When exceeded, the user will see an "bufio.Scanner: token too long" error. The default value is `bufio.MaxScanTokenSize` |

> Note: the .lock file is implemented for health-checking, but cannot be disabled yet. You must create this file in /tmp/.
