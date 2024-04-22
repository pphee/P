package main

import (
	"encoding/json"
	"fmt"
	"github.com/gocarina/gocsv"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
)

type TaxRequest struct {
	TotalIncome float64     `json:"totalIncome"`
	WHT         float64     `json:"wht"`
	Allowances  []Allowance `json:"allowances"`
}

type Allowance struct {
	AllowanceType string  `json:"allowanceType"`
	Amount        float64 `json:"amount"`
}

type TaxResponse struct {
	Tax       float64      `json:"-"`
	TaxLevels []TaxBracket `json:"taxLevel"`
}

func (tr TaxResponse) MarshalJSON() ([]byte, error) {
	taxLevels := make([]struct {
		Level string      `json:"level"`
		Tax   json.Number `json:"tax"`
	}, len(tr.TaxLevels))

	// Format each tax level to ensure decimal places
	for i, tl := range tr.TaxLevels {
		taxLevels[i] = struct {
			Level string      `json:"level"`
			Tax   json.Number `json:"tax"`
		}{
			Level: tl.Level,
			Tax:   json.Number(fmt.Sprintf("%.1f", tl.Tax)),
		}
	}

	return json.Marshal(&struct {
		Tax       json.Number `json:"tax"`
		TaxLevels []struct {
			Level string      `json:"level"`
			Tax   json.Number `json:"tax"`
		} `json:"taxLevel"`
	}{
		Tax:       json.Number(fmt.Sprintf("%.1f", tr.Tax)),
		TaxLevels: taxLevels,
	})
}

type TaxBracket struct {
	Level string  `json:"level"`
	Tax   float64 `json:"tax"`
}

type Config struct {
	PersonalDeductionDefault float64
	PersonalDeductionMax     float64
	DonationMax              float64
	KReceiptDefault          float64
	KReceiptMax              float64
}

var config = Config{
	PersonalDeductionDefault: 60000,
	PersonalDeductionMax:     100000,
	DonationMax:              100000,
	KReceiptDefault:          50000,
	KReceiptMax:              100000,
}

func calculateTotalTax(income float64) float64 {
	var tax float64

	if income > 2000000 {
		tax += (income - 2000000) * 0.35
		income = 2000000
	}
	if income > 1000000 {
		tax += (income - 1000000) * 0.2
		income = 1000000
	}
	if income > 500000 {
		tax += (income - 500000) * 0.15
		income = 500000
	}
	if income > 150000 {
		tax += (income - 150000) * 0.1
	}

	return tax
}

func calculateTaxBrackets(income float64) []TaxBracket {
	taxBrackets := []TaxBracket{
		{"0-150,000", 0},
		{"150,001-500,000", 0},
		{"500,001-1,000,000", 0},
		{"1,000,001-2,000,000", 0},
		{"2,000,001 ขึ้นไป", 0},
	}

	if income > 2000000 {
		taxBrackets[4].Tax = (income - 2000000) * 0.35
		income = 2000000
	}
	if income > 1000000 {
		taxBrackets[3].Tax = (income - 1000000) * 0.2
		income = 1000000
	}
	if income > 500000 {
		taxBrackets[2].Tax = (income - 500000) * 0.15
		income = 500000
	}
	if income > 150000 {
		taxBrackets[1].Tax = (income - 150000) * 0.1
	}

	return taxBrackets
}

func calculateIncomeTaxDetailed(income float64) (float64, []TaxBracket) {
	tax := calculateTotalTax(income)
	taxBrackets := calculateTaxBrackets(income)
	return tax, taxBrackets
}

func PostTaxCalculation(c echo.Context) error {
	var req TaxRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	var totalDeductions float64 = config.PersonalDeductionDefault
	for _, allowance := range req.Allowances {
		if allowance.Amount < 0 {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "Deduction amounts cannot be negative"})
		}
		switch allowance.AllowanceType {
		case "personal":
			if allowance.Amount > config.PersonalDeductionMax || allowance.Amount < 10000 {
				return c.JSON(http.StatusBadRequest, echo.Map{"error": "Personal deduction must be between 10,000 and " + strconv.FormatFloat(config.PersonalDeductionMax, 'f', 0, 64)})
			}
		case "donation":
			if allowance.Amount > config.DonationMax {
				allowance.Amount = config.DonationMax
			}
		case "k-receipt":
			if allowance.Amount > config.KReceiptMax && allowance.Amount > 0 {
				allowance.Amount = config.KReceiptDefault
			}
		}
		totalDeductions += allowance.Amount
	}

	if req.WHT < 0 || req.WHT > req.TotalIncome {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Invalid WHT value"})
	}

	taxableIncome := req.TotalIncome - totalDeductions
	tax, TaxBracket := calculateIncomeTaxDetailed(taxableIncome)
	tax -= req.WHT
	for i := range TaxBracket {
		TaxBracket[i].Tax -= req.WHT
		if TaxBracket[i].Tax < 0 {
			TaxBracket[i].Tax = 0
		}
	}
	res := TaxResponse{Tax: tax, TaxLevels: TaxBracket}
	return c.JSON(http.StatusOK, res)
}

func SetPersonalDeduction(c echo.Context) error {
	var req struct {
		Amount float64 `json:"amount"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Invalid JSON format or data types"})
	}

	if req.Amount < 10000 || req.Amount > config.PersonalDeductionMax {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Amount must be between 10,000 and 100,000"})
	}

	config.PersonalDeductionDefault = req.Amount

	return c.JSON(http.StatusOK, echo.Map{"personalDeduction": req.Amount})
}

func SetKReceiptDeduction(c echo.Context) error {
	var req struct {
		Amount float64 `json:"amount"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Invalid JSON format or data types"})
	}

	if req.Amount < 0 || req.Amount > config.KReceiptMax {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Amount must be between 0 and 100,000"})
	}

	config.KReceiptDefault = req.Amount

	return c.JSON(http.StatusOK, echo.Map{"kReceipt": req.Amount})
}

type TotalIncomeCsv struct {
	TotalIncome float64 `csv:"totalIncome"`
	WHT         float64 `csv:"wht"`
	Donation    float64 `csv:"donation"`
}

func taxFromFile(file io.Reader) ([]TotalIncomeCsv, error) {
	var totalIncomeCsv []TotalIncomeCsv
	if err := gocsv.Unmarshal(file, &totalIncomeCsv); err != nil {
		log.Fatal("Failed to unmarshal CSV: ", err)
		return nil, err
	}
	return totalIncomeCsv, nil
}

type TaxResponseCSV struct {
	Taxes []TaxDetail `json:"taxes"`
}

type TaxDetail struct {
	TotalIncome float64 `json:"totalIncome"`
	Tax         float64 `json:"tax"`
	TaxRefund   float64 `json:"taxRefund,omitempty"` // Omitted if zero
}

func TaxCalculationsCSVHandler(c echo.Context) error {
	file, err := c.FormFile("tax.csv") // Make sure this matches the form field name used in the file upload
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "No file uploaded"})
	}

	src, err := file.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Error opening file"})
	}
	defer src.Close()

	records, err := taxFromFile(src)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "Error reading file"})
	}

	var taxDetails []TaxDetail
	for _, record := range records {
		taxableIncome := record.TotalIncome - config.PersonalDeductionDefault - record.Donation - record.WHT
		tax, _ := calculateIncomeTaxDetailed(taxableIncome)
		netTax := tax - record.WHT

		taxRefund := 0.0
		if netTax < 0 {
			taxRefund = -netTax
			netTax = 0
		}

		taxDetails = append(taxDetails, TaxDetail{
			TotalIncome: record.TotalIncome,
			Tax:         netTax,
			TaxRefund:   taxRefund,
		})
	}

	response := TaxResponseCSV{
		Taxes: taxDetails,
	}

	return c.JSON(http.StatusOK, response)
}

type AllowanceGorm struct {
	ID            uint    `gorm:"primaryKey"`
	AllowanceType string  `gorm:"type:varchar(255);not null"`
	Amount        float64 `gorm:"type:decimal(18,2);not null"`
}

func initializeData(db *gorm.DB) error {
	allowances := []AllowanceGorm{
		{AllowanceType: "Personal", Amount: 60000.0},
		{AllowanceType: "Kreceipt", Amount: 0.0},
	}
	for _, allowance := range allowances {
		if allowance.AllowanceType != "Personal" && allowance.AllowanceType != "Kreceipt" {
			return fmt.Errorf("Invalid allowance type: %s", allowance.AllowanceType)
		}
		db.FirstOrCreate(&allowance, Allowance{AllowanceType: allowance.AllowanceType})
	}
	return nil
}

func main() {
	e := echo.New()

	// Database connection setup
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		e.Logger.Fatal("DATABASE_URL not set in environment variables")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		e.Logger.Fatal("Error connecting to database: ", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		e.Logger.Fatal("Error getting database connection: ", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.Ping(); err != nil {
		e.Logger.Fatal("Error pinging database: ", err)
	}

	// Create ENUM type
	db.Exec("CREATE TYPE allowance_type AS ENUM ('Personal', 'Kreceipt');")

	// Database migration
	if err := db.AutoMigrate(&AllowanceGorm{}); err != nil {
		log.Fatal("Failed to migrate database: ", err)
	}

	// Initialize data
	if err := initializeData(db); err != nil {
		log.Fatal("Failed to initialize data: ", err)
	}

	// Basic Auth for admin routes
	adminUser := os.Getenv("ADMIN_USERNAME")
	adminPass := os.Getenv("ADMIN_PASSWORD")
	if adminUser == "" || adminPass == "" {
		e.Logger.Fatal("Admin username or password not set in environment variables")
	}

	// API routes
	v1 := e.Group("/api/v1")
	{
		v1.POST("/tax/calculations", PostTaxCalculation)
		v1.POST("/tax/calculations/upload-csv", TaxCalculationsCSVHandler)
	}

	admin := e.Group("/admin")
	admin.Use(middleware.BasicAuth(func(username, password string, c echo.Context) (bool, error) {
		return username == adminUser && password == adminPass, nil
	}))
	{
		admin.POST("/deductions/personal", SetPersonalDeduction)
		admin.POST("/deductions/k-receipt", SetKReceiptDeduction)
	}

	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "Hello, Go Bootcamp!")
	})

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "1323"
	}
	e.Logger.Fatal(e.Start(fmt.Sprintf(":%s", port)))
}
