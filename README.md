# of-watchdog

This is a re-write of the OpenFaaS watchdog.

### Goals:
* Cleaner abstractions for maintenance
* Explore streaming for large files (beyond disk/RAM capacity)

![](https://camo.githubusercontent.com/61c169ab5cd01346bc3dc7a11edc1d218f0be3b4/68747470733a2f2f7062732e7477696d672e636f6d2f6d656469612f4447536344626c554941416f34482d2e6a70673a6c61726765)

[Original Watchdog source-code](https://github.com/openfaas/faas/tree/master/watchdog)

## Watchdog modes:

The original watchdog supported mode 3 Serializing fork and has support for mode 2 Afterburn in an open PR.

When complete this work will support all three modes and additional stretch goal of:

* multi-part forms

### 1. Streaming fork (implemented) - default.

Forks a process per request and can deal with more data than is available memory capacity - i.e. 512mb VM can process multiple GB of video.

HTTP headers cannot be sent after function starts executing due to input/output being hooked-up directly to response for streaming efficiencies. Response code is always 200 unless there is an issue forking the process. An error mid-flight will have to be picked up on the client. Multi-threaded.

* A static Content-type can be set ahead of time.

* Hard timeout: supported.

### 2. Afterburn (implemented)

Uses a single process for all requests, if that request dies the container dies.

Vastly accelerated processing speed but requires a client library for each language - HTTP over stdin/stdout. Single-threaded with a mutex.

* Limited to processing files sized as per available memory.

* HTTP headers can be set even after executing the function.

* A dynamic Content-type can be set from the client library.

* Hard timeout: not supported.

Example client libraries:

https://github.com/openfaas/nodejs-afterburn

https://github.com/alexellis/python-afterburn

https://github.com/alexellis/java-afterburn

### 3. Serializing fork (implemented in dev-branch)

Forks one process per request. Multi-threaded. Ideal for retro-fitting a CGI application handler i.e. for Flask.

Limited to processing files sized as per available memory.

Reads entire request into memory from the HTTP request. At this point we serialize or modify if required. That is then written into the stdin pipe.

* Stdout pipe is read into memory and then serialized or modified if necessary before being written back to the HTTP response.

* HTTP headers can be set even after executing the function.

* A static Content-type can be set ahead of time.

* Hard timeout: supported.

