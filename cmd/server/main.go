package main

import (
	"log"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/database"
	"fintrack-backend/internal/handlers"
	"fintrack-backend/internal/routes"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	db := database.Connect(cfg)

	database.Migrate(db)

	r := gin.Default()

	// Serve uploaded files
	r.Static("/uploads", "./uploads")

	routes.Setup(r, db, cfg)

	// Өдөр бүр AI шинжилгээ хийж notification илгээх scheduler
	handlers.StartDailyAnalysis(db)

	log.Printf("Server starting on port %s", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
