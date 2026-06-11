package proxy

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed ui.html
var uiHTML string

func (p *Proxy) HandleUI(c *fiber.Ctx) error {
	c.Type("html")
	return c.SendString(uiHTML)
}
