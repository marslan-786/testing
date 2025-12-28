package main

import (
	"log"
	"net/http"
	"os"

	"myproject/dgroup"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	// Initializations
	dClient := dgroup.NewClient()

	// ... (Previous Routes D-Group, NumberPanel, Neon) ...
	// (Mai purane routes dobara likh k lambi nahi kar raha, wo wese hi rahengy)
	
	// ----- SIRF YE WALE ADD KARNE HAIN (Previous routes k neechay) -----

	// ================= D-GROUP ROUTES =================
	r.GET("/d-group/sms", func(c *gin.Context) {
		data, err := dClient.GetSMSLogs()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})

	r.GET("/d-group/numbers", func(c *gin.Context) {
		data, err := dClient.GetNumberStats()
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
