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

	// نوٹ: اب ہم یہاں گلوبل dClient نہیں بنائیں گے
	// کیونکہ ہر ریکوئسٹ کا یوزر نیم اور پاسورڈ الگ ہوگا۔

	// ... (Previous Routes D-Group, NumberPanel, Neon if any) ...

	// ================= D-GROUP ROUTES =================

	// 1. SMS Logs Route
	r.GET("/d-group/sms", func(c *gin.Context) {
		// URL سے یوزر نیم اور پاسورڈ اٹھائیں (?u=...&p=...)
		username := c.Query("u")
		password := c.Query("p")

		// چیک کریں کہ لنک میں پاسورڈ موجود ہے یا نہیں
		if username == "" || password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'u' (username) or 'p' (password) in URL"})
			return
		}

		// یہاں نیا کلائنٹ بنائیں اسی یوزر کے لیے
		client := dgroup.NewClient(username, password)

		data, err := client.GetSMSLogs()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})

	// 2. Number Stats Route
	r.GET("/d-group/numbers", func(c *gin.Context) {
		// URL سے یوزر نیم اور پاسورڈ اٹھائیں
		username := c.Query("u")
		password := c.Query("p")

		if username == "" || password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'u' (username) or 'p' (password) in URL"})
			return
		}

		// یہاں نیا کلائنٹ بنائیں
		client := dgroup.NewClient(username, password)

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
