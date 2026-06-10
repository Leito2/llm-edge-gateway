package auth

import (
	"crypto/subtle"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func RequireAPIKey(expected string) fiber.Handler {
	expectedBytes := []byte(expected)
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "missing or malformed Authorization header (expected: Bearer <key>)",
			})
		}
		got := []byte(strings.TrimPrefix(auth, "Bearer "))
		if len(got) != len(expectedBytes) ||
			subtle.ConstantTimeCompare(got, expectedBytes) != 1 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid api key",
			})
		}
		return c.Next()
	}
}
