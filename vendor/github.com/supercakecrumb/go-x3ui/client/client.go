package client

import (
	"crypto/tls"
	"log/slog"
	"net/http/cookiejar"
	"time"

	"github.com/go-resty/resty/v2"
)

type Client struct {
	BaseURL  string
	Logger   *slog.Logger
	Resty    *resty.Client
	Username string
	Password string
}

func NewClient(baseURL, username, password string, insecure bool, logger *slog.Logger) *Client {
	jar, _ := cookiejar.New(nil)

	restyClient := resty.New().
		SetBaseURL(baseURL).
		SetTLSClientConfig(&tls.Config{InsecureSkipVerify: insecure}).
		SetCookieJar(jar).
		SetTimeout(10 * time.Second)

	return &Client{
		BaseURL:  baseURL,
		Logger:   logger,
		Resty:    restyClient,
		Username: username,
		Password: password,
	}
}
