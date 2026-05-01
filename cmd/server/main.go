package main

import (
	"log"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/web/handler"
)

func newServer() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.GET("/login", handler.LoginGet)
	return e
}

func main() {
	e := newServer()
	if err := e.Start(":8080"); err != nil {
		log.Fatal(err)
	}
}
