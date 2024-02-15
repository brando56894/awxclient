##Various scripts to make building easier

**musl-debug.sh**: Compiles the binary with debugging symbols enabled, for use with [delve](https://golang.cafe/blog/golang-debugging-with-delve.html). Links against the MUSL C library instead of libc, which prevents static linking issues during compilation when building locally and executing on a remote host.

**musl.sh**: Compiles the binary and links against the MUSL C library instead of libc, which prevents static linking issues during compilation when building locally and executing on a remote host.

**buildrpm.sh**: As the name implies, this will build the RPM.
