package auth

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func newApp(expected string) *fiber.App {
	app := fiber.New()
	app.Use(RequireAPIKey(expected))
	app.Get("/protected", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func TestAuth_NoHeader(t *testing.T) {
	app := newApp("secret-key-1234567890")
	req := httptest.NewRequest("GET", "/protected", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_WrongScheme(t *testing.T) {
	app := newApp("secret-key-1234567890")
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_WrongKey(t *testing.T) {
	app := newApp("secret-key-1234567890")
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer wrong-key-1234567890")
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_CorrectKey(t *testing.T) {
	app := newApp("secret-key-1234567890")
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer secret-key-1234567890")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_DifferentLength(t *testing.T) {
	app := newApp("secret-key-1234567890")
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer short")
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401 (different length)", resp.StatusCode)
	}
}

func TestAuth_ErrorBody(t *testing.T) {
	app := newApp("secret-key-1234567890")
	req := httptest.NewRequest("GET", "/protected", nil)
	resp, _ := app.Test(req)
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "error") {
		t.Errorf("body should contain 'error', got: %s", body)
	}
}
