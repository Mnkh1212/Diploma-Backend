package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type NotificationHandler struct {
	DB *gorm.DB
}

func NewNotificationHandler(db *gorm.DB) *NotificationHandler {
	return &NotificationHandler{DB: db}
}

// SavePushToken - хэрэглэгчийн push token хадгалах
func (h *NotificationHandler) SavePushToken(c *gin.Context) {
	userID := c.GetUint("user_id")

	var input struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.DB.Model(&models.User{}).Where("id = ?", userID).Update("push_token", input.Token)
	c.JSON(http.StatusOK, gin.H{"message": "Push token saved"})
}

// ListNotifications - хэрэглэгчийн мэдэгдлүүд
func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	userID := c.GetUint("user_id")

	var notifications []models.Notification
	h.DB.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(50).
		Find(&notifications)

	c.JSON(http.StatusOK, notifications)
}

// MarkRead - мэдэгдэл уншсан болгох
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID := c.GetUint("user_id")
	notifID := c.Param("id")

	h.DB.Model(&models.Notification{}).
		Where("id = ? AND user_id = ?", notifID, userID).
		Update("is_read", true)

	c.JSON(http.StatusOK, gin.H{"message": "Marked as read"})
}

// MarkAllRead - бүх мэдэгдлийг уншсан болгох
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID := c.GetUint("user_id")

	h.DB.Model(&models.Notification{}).
		Where("user_id = ? AND is_read = ?", userID, false).
		Update("is_read", true)

	c.JSON(http.StatusOK, gin.H{"message": "All marked as read"})
}

// StartDailyAnalysis - өдөр бүр AI шинжилгээ хийж notification илгээх scheduler
func StartDailyAnalysis(db *gorm.DB) {
	go func() {
		// Эхлэхэд 1 минут хүлээнэ (server бүрэн ачаалах хүртэл)
		time.Sleep(1 * time.Minute)

		for {
			log.Println("[AI Analysis] Starting daily analysis for all users...")
			runAnalysisForAllUsers(db)

			// Дараагийн өдрийн 09:00 хүртэл хүлээнэ
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, now.Location())
			sleepDuration := next.Sub(now)
			log.Printf("[AI Analysis] Next run at %s (in %s)", next.Format("2006-01-02 15:04"), sleepDuration.Round(time.Minute))
			time.Sleep(sleepDuration)
		}
	}()
}

func runAnalysisForAllUsers(db *gorm.DB) {
	var users []models.User
	db.Where("push_token != ''").Find(&users)

	for _, user := range users {
		insights := analyzeUserFinances(db, user.ID)
		for _, insight := range insights {
			// DB-д хадгалах
			notif := models.Notification{
				UserID: user.ID,
				Title:  insight.Title,
				Body:   insight.Body,
				Type:   insight.Type,
			}
			db.Create(&notif)

			// Expo push notification илгээх
			sendExpoPushNotification(user.PushToken, insight.Title, insight.Body)
		}
	}
	log.Printf("[AI Analysis] Completed for %d users", len(users))
}

type insight struct {
	Title string
	Body  string
	Type  string
}

func analyzeUserFinances(db *gorm.DB, userID uint) []insight {
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startOfLastMonth := startOfMonth.AddDate(0, -1, 0)

	var totalBalance float64
	db.Model(&models.Account{}).Where("user_id = ?", userID).
		Select("COALESCE(SUM(balance), 0)").Scan(&totalBalance)

	var monthlyIncome, monthlyExpenses float64
	db.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "income", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&monthlyIncome)
	db.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&monthlyExpenses)

	var lastMonthExpenses float64
	db.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", startOfLastMonth, startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&lastMonthExpenses)

	// Өнөөдрийн зарлага
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var todayExpenses float64
	db.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", todayStart).
		Select("COALESCE(SUM(amount), 0)").Scan(&todayExpenses)

	// Хамгийн их зарлагатай ангилал
	type catExp struct {
		Name  string
		Total float64
	}
	var topCat []catExp
	db.Model(&models.Transaction{}).
		Select("categories.name, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.type = ? AND transactions.date >= ?", userID, "expense", startOfMonth).
		Group("categories.name").Order("total DESC").Limit(3).Scan(&topCat)

	// Төсвийн анхааруулга
	var budgets []models.Budget
	db.Preload("Category").
		Where("user_id = ? AND month = ? AND year = ?", userID, int(now.Month()), now.Year()).
		Find(&budgets)

	var results []insight

	// 1. Үлдэгдэл мэдээлэл
	results = append(results, insight{
		Title: "💰 Өнөөдрийн санхүүгийн тойм",
		Body:  fmt.Sprintf("Нийт үлдэгдэл: %.0f₮. Энэ сарын орлого: %.0f₮, зарлага: %.0f₮.", totalBalance, monthlyIncome, monthlyExpenses),
		Type:  "insight",
	})

	// 2. Хэмнэлтийн анализ
	if monthlyIncome > 0 {
		savingsRate := ((monthlyIncome - monthlyExpenses) / monthlyIncome) * 100
		if savingsRate >= 30 {
			results = append(results, insight{
				Title: "🎉 Маш сайн хэмнэлт!",
				Body:  fmt.Sprintf("Та орлогынхоо %.0f%%-ийг хэмнэж байна. Хөрөнгө оруулалтад зарцуулах боломжтой.", savingsRate),
				Type:  "tip",
			})
		} else if savingsRate < 10 {
			results = append(results, insight{
				Title: "⚠️ Хэмнэлт бага байна",
				Body:  fmt.Sprintf("Хэмнэлтийн хувь %.0f%%. Орлогынхоо дор хаяж 20%%-ийг хэмнэхийг зорьж үзээрэй.", savingsRate),
				Type:  "warning",
			})
		}
	}

	// 3. Зарлагын өөрчлөлт
	if lastMonthExpenses > 0 {
		change := ((monthlyExpenses - lastMonthExpenses) / lastMonthExpenses) * 100
		if change > 20 {
			results = append(results, insight{
				Title: "📈 Зарлага нэмэгдсэн",
				Body:  fmt.Sprintf("Энэ сарын зарлага өмнөх сараас %.0f%%-иар нэмэгдсэн байна. Шалтгааныг шалгаарай.", math.Abs(change)),
				Type:  "warning",
			})
		} else if change < -10 {
			results = append(results, insight{
				Title: "✅ Зарлага буурсан",
				Body:  fmt.Sprintf("Баяр хүргэе! Зарлага өмнөх сараас %.0f%%-иар буурсан.", math.Abs(change)),
				Type:  "tip",
			})
		}
	}

	// 4. Төсвийн анхааруулга
	for _, b := range budgets {
		if b.Amount > 0 {
			pct := (b.Spent / b.Amount) * 100
			catName := "Нийт"
			if b.Category.Name != "" {
				catName = b.Category.Name
			}
			if pct >= 100 {
				results = append(results, insight{
					Title: fmt.Sprintf("🚨 %s төсөв хэтэрсэн!", catName),
					Body:  fmt.Sprintf("%.0f₮-ийн төсвөөс %.0f₮ зарцуулсан (%.0f%%).", b.Amount, b.Spent, pct),
					Type:  "warning",
				})
			} else if pct >= 80 {
				results = append(results, insight{
					Title: fmt.Sprintf("⚠️ %s төсөв дуусах дөхсөн", catName),
					Body:  fmt.Sprintf("Төсвийн %.0f%% зарцуулагдсан. %.0f₮ үлдсэн.", pct, b.Amount-b.Spent),
					Type:  "warning",
				})
			}
		}
	}

	// 5. Хамгийн их зарлагатай ангилал
	if len(topCat) > 0 {
		results = append(results, insight{
			Title: "📊 Зарлагын шинжилгээ",
			Body:  fmt.Sprintf("Энэ сарын хамгийн их зарлагатай ангилал: %s (%.0f₮). Төсөв тогтоож хяналт тавьж болно.", topCat[0].Name, topCat[0].Total),
			Type:  "insight",
		})
	}

	return results
}

// Expo Push Notification илгээх
func sendExpoPushNotification(token, title, body string) {
	if token == "" {
		return
	}

	payload := map[string]interface{}{
		"to":    token,
		"title": title,
		"body":  body,
		"sound": "default",
	}

	jsonData, _ := json.Marshal(payload)

	resp, err := http.Post(
		"https://exp.host/--/api/v2/push/send",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		log.Printf("[Push] Failed to send: %v", err)
		return
	}
	defer resp.Body.Close()
}
