package main

// configuredAddress centralizes the PORT-to-loopback policy used by the entrypoint.
func configuredAddress(port string) (string, error) {
	return addressFromEnvironment(port)
}
