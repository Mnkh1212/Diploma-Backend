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
		&models.Notification{},
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
		{Name: "Хоол", Icon: "restaurant", Color: "#FF6B6B", Type: "expense"},
		{Name: "Дэлгүүр", Icon: "cart", Color: "#4ECDC4", Type: "expense"},
		{Name: "Тээвэр", Icon: "car", Color: "#45B7D1", Type: "expense"},
		{Name: "Хүнсний бараа", Icon: "basket", Color: "#96CEB4", Type: "expense"},
		{Name: "Эрүүл мэнд", Icon: "medical", Color: "#FFEAA7", Type: "expense"},
		{Name: "Аялал", Icon: "airplane", Color: "#DDA0DD", Type: "expense"},
		{Name: "Такси", Icon: "car-sport", Color: "#F39C12", Type: "expense"},
		{Name: "Орон сууц", Icon: "home", Color: "#E74C3C", Type: "expense"},
		{Name: "Даатгал", Icon: "shield", Color: "#3498DB", Type: "expense"},
		{Name: "Интернет", Icon: "wifi", Color: "#9B59B6", Type: "expense"},
		{Name: "Зугаа цэнгэл", Icon: "game-controller", Color: "#E056A0", Type: "expense"},
		{Name: "Боловсрол", Icon: "school", Color: "#00B894", Type: "expense"},
		{Name: "Цалин", Icon: "cash", Color: "#00B894", Type: "income"},
		{Name: "Фрийланс", Icon: "laptop", Color: "#6C5CE7", Type: "income"},
		{Name: "Хөрөнгө оруулалт", Icon: "trending-up", Color: "#FDCB6E", Type: "income"},
		{Name: "Шилжүүлэг", Icon: "swap-horizontal", Color: "#74B9FF", Type: "income"},
		{Name: "Бусад орлого", Icon: "add-circle", Color: "#A29BFE", Type: "income"},
	}

	db.Create(&categories)
	log.Println("Categories seeded successfully")
}
