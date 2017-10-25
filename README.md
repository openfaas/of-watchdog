# of-watchdog
Prototype re-write of OpenFaaS watchdog

Goals:
* Cleaner abstractions for maintenance
* Explore streaming for large files (beyond disk/RAM capacity)

Watchdog modes:

* Streaming fork (implemented) - default.

Forks a process per request and can deal with more data than is available memory capacity - i.e. 512mb VM can process multiple GB of video.

HTTP headers cannot be sent after function starts executing due to input/output being hooked-up directly to response for streaming efficiencies. Response code is always 200 unless there is an issue forking the process. An error mid-flight will have to be picked up on the client. Multi-threaded.

A static Content-type can be set ahead of time.

Hard timeout: supported.

* afterburn (implemented)

Uses a single process for all requests, if that request dies the container dies.

Vastly accelerated processing speed but requires a client library for each language - HTTP over stdin/stdout. Single-threaded with a mutex.

Limited to processing files sized as per available memory.

HTTP headers can be set even after executing the function.

A dynamic Content-type can be set from the client library.

Hard timeout: not supported.

* Serializing fork (not implemented)

Forks one process per request. Multi-threaded. Ideal for retro-fitting a CGI application handler i.e. for Flask.

Limited to processing files sized as per available memory.

Reads entire request into memory from the HTTP request. At this point we serialize or modify if required. That is then written into the stdin pipe.

Stdout pipe is read into memory and then serialized or modified if necessary before being written back to the HTTP response.

HTTP headers can be set even after executing the function.

A static Content-type can be set ahead of time.

Hard timeout: supported.
