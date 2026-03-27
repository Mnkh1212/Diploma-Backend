package database

import (
	"fmt"
	"log"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Connect(cfg *config.Config) *gorm.DB {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	DB = db
	log.Println("Database connected successfully")
	return db
}

func Migrate(db *gorm.DB) {
	err := db.AutoMigrate(
		&models.User{},
		&models.Account{},
		&models.Category{},
		&models.Transaction{},
		&models.Budget{},
		&models.ScheduledPayment{},
		&models.AIChat{},
		&models.AIMessage{},
		&models.ActivityLog{},
	)
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
	log.Println("Database migrated successfully")

	seedCategories(db)
}

func seedCategories(db *gorm.DB) {
	var count int64
	db.Model(&models.Category{}).Count(&count)
	if count > 0 {
		return
	}

	categories := []models.Category{
		{Name: "Food", Icon: "restaurant", Color: "#FF6B6B", Type: "expense"},
		{Name: "Shopping", Icon: "cart", Color: "#4ECDC4", Type: "expense"},
		{Name: "Transport", Icon: "car", Color: "#45B7D1", Type: "expense"},
		{Name: "Groceries", Icon: "basket", Color: "#96CEB4", Type: "expense"},
		{Name: "Health", Icon: "medical", Color: "#FFEAA7", Type: "expense"},
		{Name: "Travel", Icon: "airplane", Color: "#DDA0DD", Type: "expense"},
		{Name: "Taxi", Icon: "taxi", Color: "#F39C12", Type: "expense"},
		{Name: "Home service fee", Icon: "home", Color: "#E74C3C", Type: "expense"},
		{Name: "Car insurance", Icon: "shield", Color: "#3498DB", Type: "expense"},
		{Name: "Internet", Icon: "wifi", Color: "#9B59B6", Type: "expense"},
		{Name: "Entertainment", Icon: "game-controller", Color: "#E056A0", Type: "expense"},
		{Name: "Education", Icon: "school", Color: "#00B894", Type: "expense"},
		{Name: "Salary", Icon: "cash", Color: "#00B894", Type: "income"},
		{Name: "Freelance", Icon: "laptop", Color: "#6C5CE7", Type: "income"},
		{Name: "Investment", Icon: "trending-up", Color: "#FDCB6E", Type: "income"},
		{Name: "Transfer", Icon: "swap-horizontal", Color: "#74B9FF", Type: "income"},
		{Name: "Other Income", Icon: "add-circle", Color: "#A29BFE", Type: "income"},
	}

	db.Create(&categories)
	log.Println("Categories seeded successfully")
}
