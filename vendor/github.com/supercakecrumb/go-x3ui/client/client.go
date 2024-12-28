package client

import (
	"crypto/tls"
	"log/slog"
	"net/http/cookiejar"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

var sessionExpiryDelta = 5 * time.Minute // or any duration you prefer

type Client struct {
	BaseURL       string
	Logger        *slog.Logger
	Resty         *resty.Client
	Username      string
	Password      string
	LastLoginTime time.Time
	loginMutex    sync.Mutex
}

func NewClient(baseURL, username, password string, insecure bool, logger *slog.Logger) *Client {
	jar, _ := cookiejar.New(nil)

	client := &Client{
		BaseURL:    baseURL,
		Logger:     logger,
		Username:   username,
		Password:   password,
		Resty:      resty.New(),
		loginMutex: sync.Mutex{},
	}

	client.Resty.
		SetBaseURL(baseURL).
		SetTLSClientConfig(&tls.Config{InsecureSkipVerify: insecure}).
		SetCookieJar(jar).
		SetTimeout(10 * time.Second).
		OnBeforeRequest(client.checkAndRefreshSession).
		OnAfterResponse(func(c *resty.Client, resp *resty.Response) error {
			if resp.StatusCode() == 401 || resp.StatusCode() == 403 {
				client.Logger.Info("Received unauthorized response, re-logging in")

				err := client.Login()
				if err != nil {
					return err
				}

				// Retry the request
				resp.Request.Attempt = 0 // Reset the attempt counter
				newResp, err := resp.Request.Send()
				if err != nil {
					return err
				}

				// Replace the original response with the new one
				*resp = *newResp
			}

			return nil
		})

	return client
}
