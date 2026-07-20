package main

import "github.com/labstack/echo/v4"

func getIndex(c echo.Context) error {
	return c.File(frontendContentsPath + "/index.html")
}
