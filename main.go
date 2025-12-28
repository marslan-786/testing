package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"myproject/dgroup"
)

func main() {
	r := gin.Default()

	// ================= D-GROUP ROUTES =================

	r.GET("/d-group/sms", func(c *gin.Context) {
		username := c.Query("u")
		password := c.Query("p")

		if username == "" || password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'u' or 'p'"})
			return
		}

		// CHANGE: NewClient -> GetSession
		// یہ فنکشن میموری چیک کرے گا، اگر سیشن ہے تو وہی دے گا
		client := dgroup.GetSession(username, password)

		data, err := client.GetSMSLogs()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})

	r.GET("/d-group/numbers", func(c *gin.Context) {
		username := c.Query("u")
		password := c.Query("p")

		if username == "" || password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'u' or 'p'"})
			return
		}

		// CHANGE: NewClient -> GetSession
		client := dgroup.GetSession(username, password)

		data, err := client.GetNumberStats()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})

	// ================= SERVER START =================
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Println("Server running on port: " + port)
	r.Run("0.0.0.0:" + port)
}
