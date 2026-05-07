package database

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

// categoriesJSON - анхдагч ангиллын жагсаалт. JSON файл нь сонгож засах,
// шинэ ангилал нэмэх боломжтойгоор хадгална. Build-ийн үед binary-д шингэнэ
// (ажиллаж буй сервер дээр файл системээс хамаардаггүй).
//
//go:embed categories.json
var categoriesJSON []byte

func Connect(cfg *config.Config) *gorm.DB {
	sslmode := "disable"
	if cfg.DBHost != "localhost" && cfg.DBHost != "postgres" {
		sslmode = "require"
	}
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, sslmode,
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
		&models.AIAnalysis{},
	)
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
	log.Println("Database migrated successfully")

	syncCategories(db)
}

// syncCategories - categories.json-той DB-г синхрончилно.
//
// Логик:
//   - JSON-д байгаа боловч DB-д байхгүй ангилал бүрийг шинээр үүсгэнэ
//   - DB-д байгаа боловч JSON-д байхгүй ангиллыг хэвээр үлдээнэ (хэрэглэгч
//     нэмсэн байж болзошгүй; тэр гүйлгээтэй холбоотой байх ч магадтай тул
//     устгах нь аюултай)
//   - Icon/color/type зөрвөл JSON-аас DB-руу шинэчилнэ (нэрээр харьцуулна)
//
// Тиймээс категори нэмэх, өнгийг солих гэх мэт зүйлсийг database.go-г
// хөндөхгүйгээр зөвхөн categories.json-г засах замаар хийж болно.
func syncCategories(db *gorm.DB) {
	var seed []models.Category
	if err := json.Unmarshal(categoriesJSON, &seed); err != nil {
		log.Printf("categories.json parse failed: %v — синхрон алгасав", err)
		return
	}
	if len(seed) == 0 {
		return
	}

	var existing []models.Category
	db.Find(&existing)
	existingByName := make(map[string]models.Category, len(existing))
	for _, c := range existing {
		existingByName[c.Name] = c
	}

	var toCreate []models.Category
	var updated, kept int
	for _, s := range seed {
		if cur, ok := existingByName[s.Name]; ok {
			// Icon/color/type нь JSON-аас өөр бол шинэчлэнэ
			if cur.Icon != s.Icon || cur.Color != s.Color || cur.Type != s.Type {
				db.Model(&models.Category{}).Where("id = ?", cur.ID).Updates(map[string]interface{}{
					"icon":  s.Icon,
					"color": s.Color,
					"type":  s.Type,
				})
				updated++
			} else {
				kept++
			}
		} else {
			toCreate = append(toCreate, s)
		}
	}

	if len(toCreate) > 0 {
		db.Create(&toCreate)
	}

	log.Printf("categories sync: %d created, %d updated, %d kept (JSON %d, DB %d)",
		len(toCreate), updated, kept, len(seed), len(existing))
}
