package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type TransactionHandler struct {
	DB *gorm.DB
}

func NewTransactionHandler(db *gorm.DB) *TransactionHandler {
	return &TransactionHandler{DB: db}
}

func (h *TransactionHandler) Create(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req models.CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format, use YYYY-MM-DD"})
		return
	}

	// Ангилал шийдэх: CategoryID өгсөн бол түүгээр; үгүй CategoryName бол
	// find-or-create; хоосон бол "Бусад" руу буцаана.
	categoryID, catErr := h.resolveCategory(req.CategoryID, req.CategoryName, req.Type)
	if catErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": catErr.Error()})
		return
	}

	tx := models.Transaction{
		UserID:      userID,
		AccountID:   req.AccountID,
		CategoryID:  categoryID,
		Amount:      req.Amount,
		Type:        req.Type,
		Description: req.Description,
		Date:        date,
	}

	if err := h.DB.Create(&tx).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create transaction"})
		return
	}

	// Update account balance
	var account models.Account
	if err := h.DB.First(&account, req.AccountID).Error; err == nil {
		if req.Type == "income" {
			account.Balance += req.Amount
		} else {
			account.Balance -= req.Amount
		}
		h.DB.Save(&account)
	}

	// Update budget spent if expense
	if req.Type == "expense" {
		var budget models.Budget
		if err := h.DB.Where("user_id = ? AND category_id = ? AND month = ? AND year = ?",
			userID, categoryID, int(date.Month()), date.Year()).First(&budget).Error; err == nil {
			budget.Spent += req.Amount
			h.DB.Save(&budget)
		}
	}

	h.DB.Preload("Category").Preload("Account").First(&tx, tx.ID)
	c.JSON(http.StatusCreated, tx)
	LogActivity(h.DB, userID, "create_transaction", "transaction", tx.ID, fmt.Sprintf(`{"amount":%.2f,"type":"%s"}`, tx.Amount, tx.Type), "success", c.ClientIP())
}

// resolveCategory - гүйлгээ үүсгэх үед ангиллыг шийднэ:
//
//  1. catID > 0 бол түүгээр шууд хайна (DB-д байхгүй бол алдаа)
//  2. catID == 0 бөгөөд name өгсөн бол: case-insensitive trim хийгээд
//     ижил нэр + type-тай ангилал хайна. Олдохгүй бол шинээр үүсгэнэ.
//  3. catID == 0 + name хоосон бол "Бусад" нэртэй ангиллыг буцаана.
//     "Бусад" байхгүй бол шинээр (expense type-аар) үүсгэнэ.
func (h *TransactionHandler) resolveCategory(catID uint, name, txType string) (uint, error) {
	if catID > 0 {
		var existing models.Category
		if err := h.DB.First(&existing, catID).Error; err != nil {
			return 0, fmt.Errorf("category_id %d not found", catID)
		}
		return existing.ID, nil
	}

	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = "Бусад"
	}

	// Найдвартай тохирол: ижил нэр (case-insensitive) + type
	var existing models.Category
	err := h.DB.Where("LOWER(name) = LOWER(?) AND type = ?", cleanName, txType).
		First(&existing).Error
	if err == nil {
		return existing.ID, nil
	}

	// Шинэ ангилал үүсгэх
	newCat := models.Category{
		Name:  cleanName,
		Type:  txType,
		Icon:  "pricetag-outline",
		Color: pickRandomColor(cleanName),
	}
	if err := h.DB.Create(&newCat).Error; err != nil {
		return 0, fmt.Errorf("category create failed: %w", err)
	}
	return newCat.ID, nil
}

// pickRandomColor - ангиллын нэрнээс тогтмол өнгө сонгоно (нэг нэрэнд үргэлж
// нэг өнгө). Hash-based simple deterministic.
func pickRandomColor(seed string) string {
	palette := []string{
		"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4", "#FFEAA7",
		"#DDA0DD", "#F39C12", "#E74C3C", "#3498DB", "#9B59B6",
		"#E056A0", "#00B894", "#6C5CE7", "#FDCB6E", "#74B9FF", "#A29BFE",
	}
	var sum int
	for _, r := range seed {
		sum += int(r)
	}
	return palette[sum%len(palette)]
}

func (h *TransactionHandler) List(c *gin.Context) {
	userID := c.GetUint("user_id")

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	txType := c.Query("type")
	search := c.Query("search")
	accountID := c.Query("account_id")

	offset := (page - 1) * limit

	query := h.DB.Where("user_id = ?", userID)

	if txType != "" {
		query = query.Where("type = ?", txType)
	}
	if search != "" {
		query = query.Where("description ILIKE ?", "%"+search+"%")
	}
	if accountID != "" {
		query = query.Where("account_id = ?", accountID)
	}

	var total int64
	query.Model(&models.Transaction{}).Count(&total)

	var transactions []models.Transaction
	query.Preload("Category").Preload("Account").
		Order("date DESC, created_at DESC").
		Offset(offset).Limit(limit).
		Find(&transactions)

	c.JSON(http.StatusOK, gin.H{
		"data":  transactions,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

func (h *TransactionHandler) Get(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var tx models.Transaction
	if err := h.DB.Preload("Category").Preload("Account").
		Where("user_id = ?", userID).First(&tx, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}

	c.JSON(http.StatusOK, tx)
}

func (h *TransactionHandler) Delete(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var tx models.Transaction
	if err := h.DB.Where("user_id = ?", userID).First(&tx, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}

	// Reverse balance change
	var account models.Account
	if err := h.DB.First(&account, tx.AccountID).Error; err == nil {
		if tx.Type == "income" {
			account.Balance -= tx.Amount
		} else {
			account.Balance += tx.Amount
		}
		h.DB.Save(&account)
	}

	h.DB.Delete(&tx)
	c.JSON(http.StatusOK, gin.H{"message": "Transaction deleted"})
	idUint, _ := strconv.ParseUint(id, 10, 32)
	LogActivity(h.DB, userID, "delete_transaction", "transaction", uint(idUint), "", "success", c.ClientIP())
}
