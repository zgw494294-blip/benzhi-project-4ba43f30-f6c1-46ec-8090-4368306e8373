package main

import (
	"net/http"
	"time"
)

func selfcheckHTTPClient() *http.Client {
	return &http.Client{Timeout: 4 * time.Second}
}
