package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/MarkKremer/microphone"
	"github.com/atotto/clipboard"
	"github.com/gopxl/beep"
	"github.com/gopxl/beep/wav"
)

type AppState struct {
	UserInput string `json:"userInput"`
	UserLang  string `json:"userLang"`
	UserRate  string `json:"userRate"`
}

var (
	userLang  string
	userInput string
	userRate  string
	entry     *widget.Entry
	a         = app.New()
	w         = a.NewWindow("Whisper")
	recording bool
	stopChan  chan struct{}
	timer     *time.Timer
	startTime time.Time
	data      struct {
		Text string `json:"text"`
	}
	state     = AppState{}
	configDir string
	filename  string
)

func main() {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	configDir = filepath.Join(usr.HomeDir, ".config", "whisper")
	err = os.MkdirAll(configDir, 0755)
	if err != nil {
		log.Fatal(err)
	}

	state := loadState()

	w.Resize(fyne.NewSize(400, 300))

	str := binding.NewString()
	str.Set(" ")

	text := widget.NewLabelWithData(str)
	text.Wrapping = fyne.TextWrapWord

	timer := widget.NewLabel("00:00:00")

	headerKey := widget.NewLabel("OpenAI key:")
	headerLang := widget.NewLabel("Language:")
	headerRate := widget.NewLabel("Sample rate:")

	apikey := binding.BindString(&state.UserInput)
	entry := widget.NewEntryWithData(apikey)

	entry.OnChanged = func(text string) {
		state.UserInput = text
		userInput = text
	}

	languageOptions := []string{"cs", "en"}
	languageBinding := binding.BindString(&state.UserLang)

	language := widget.NewSelect(languageOptions, func(selected string) {
		languageBinding.Set(selected)
		state.UserLang = selected
		log.Println("Selected language: " + selected)
	})

	language.SetSelected(state.UserLang)

	sampleRateOptions := []string{"44100", "48000"}
	sampleRateBinding := binding.BindString(&state.UserRate)

	sampleRate := widget.NewSelect(sampleRateOptions, func(selected string) {
		sampleRateBinding.Set(selected)
		state.UserRate = selected
		log.Println("Selected sample rate: " + selected)
	})

	sampleRate.SetSelected(state.UserRate)

	button1 := widget.NewButton("Start recording", func() {
		filename = generateFilename()
		log.Println("Recording started", filename)
		startTimer(timer)
		startRecording(filename)
	})

	button2 := widget.NewButton("Stop recording", func() {
		if !recording {
			return
		}
		stopTimer()
		stopRecording()
		log.Println("Recording stopped", filename)
		response := apiCall(filename)
		correctedText := correctText(response)

		str.Set(correctedText)
		clipboardWrite(correctedText)
	})

	appContent := container.NewVBox(button1, button2, timer, text)
	settingsContent := container.NewVBox(headerKey, entry, headerLang, language, headerRate, sampleRate)

	tabs := container.NewAppTabs(
		container.NewTabItem("App", appContent),
		container.NewTabItem("Settings", settingsContent),
	)

	w.SetContent(tabs)
	w.ShowAndRun()

	saveState()
}

func startTimer(label *widget.Label) {
	stopTimer()

	startTime = time.Now()
	timer = time.AfterFunc(0, func() {
		elapsed := time.Since(startTime).Truncate(time.Second)
		label.SetText(elapsed.String())
		timer.Reset(time.Second)
	})
}

func stopTimer() {
	if timer != nil {
		timer.Stop()
		timer = nil
	}
}

func clipboardWrite(output string) {
	err := clipboard.WriteAll(output)
	if err != nil {
		log.Fatal(err)
	}
}

func apiCall(filename string) string {
	audioFilePath := filename
	log.Println("audioFilePath: " + audioFilePath)
	url := "https://api.openai.com/v1/audio/transcriptions"
	file, err := os.Open(audioFilePath)
	if err != nil {
		log.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", audioFilePath)
	if err != nil {
		log.Fatal(err)
	}

	_, err = io.Copy(part, file)
	if err != nil {
		log.Fatal(err)
	}

	_ = writer.WriteField("model", "whisper-1")
	_ = writer.WriteField("language", state.UserLang)

	err = writer.Close()
	if err != nil {
		log.Fatal(err)
	}

	request, err := http.NewRequest("POST", url, body)
	if err != nil {
		log.Fatal(err)
	}

	openaiKey := userInput
	log.Println("openaiKey: " + openaiKey)

	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+openaiKey)

	client := http.Client{}
	response, err := client.Do(request)
	if err != nil {
		log.Fatal(err)
	}

	defer response.Body.Close()

	fmt.Println("Status: ", response.Status)

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}

	err1 := json.Unmarshal([]byte(responseBody), &data)
	if err1 != nil {
		log.Println(err)
	}
	unformattedText := data.Text
	log.Println("unformattedText: " + unformattedText)
	return string(unformattedText)
}

func correctText(text string) string {
	var role string

	if state.UserLang == "en" {
		role = "You are an editor that corrects errors in speech to text transcription."
	} else {
		role = "Jste editor, který opravuje chyby v přepisu řeči na text."
	}
	token := userInput
	url := "https://api.openai.com/v1/chat/completions"
	model := "gpt-3.5-turbo"
	body := []byte(fmt.Sprintf(`{
		"model": "%s",
		"messages": [
		  {
			"role": "system" ,
			"content": "%s"
		  },
		  {
			"role": "user",
			"content": "%s"
		  }
		]
	  }`, model, role, text))

	log.Println("body: " + string(body))
	request, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		log.Fatal(err)
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)

	client := http.Client{}
	response, err := client.Do(request)
	if err != nil {
		log.Fatal(err)
	}

	defer response.Body.Close()

	fmt.Println("Status: ", response.Status)

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message""`
		} `json:"choices"`
	}

	err1 := json.Unmarshal([]byte(responseBody), &result)
	if err1 != nil {
		log.Println(err)
	}

	var message string

	if len(result.Choices) > 0 {
		message := result.Choices[0].Message.Content
		log.Println("message: " + message)
		return string(message)
	} else {
		log.Println("No message")
	}

	return message
}

func recordAudio(stopChan <-chan struct{}, filename string) {
	log.Println("Recording audio into " + filename)
	err := microphone.Init()
	if err != nil {
		log.Fatal(err)
	}
	defer microphone.Terminate()

	userRate, err := strconv.Atoi(state.UserRate)
	if err != nil {
		log.Fatal(err)
	}
	stream, format, err := microphone.OpenDefaultStream(beep.SampleRate(userRate), 1)
	if err != nil {
		log.Fatal(err)
	}

	defer stream.Close()

	f, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	go func() {
		<-stopChan
		log.Println("Stopping recording")
		stream.Stop()
	}()

	stream.Start()

	err = wav.Encode(f, stream, format)
	if err != nil {
		log.Fatal(err)
	}

	os.Remove(filename)
}

func startRecording(filename string) {
	log.Println("start recording")
	log.Println(filename)
	recording = true
	stopChan = make(chan struct{})
	go recordAudio(stopChan, filename)
}

func stopRecording() {
	if recording {
		log.Println("stop recording")
		close(stopChan)
		recording = false
	} else {
		log.Println("not recording")
	}

}

func loadState() *AppState {
	filePath := filepath.Join(configDir, "state.json")
	file, err := os.Open(filePath)
	if err != nil {
		return &AppState{}
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&state)
	if err != nil {
		fmt.Println("Error decoding state:", err)
	}
	return &state
}

func saveState() {
	filePath := filepath.Join(configDir, "state.json")
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Println("Error creating state file:", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(state)
	if err != nil {
		fmt.Println("Error encoding state:", err)
	}
}

func generateFilename() string {
	filename := fmt.Sprintf("/tmp/output_%d.wav", time.Now().Unix())
	return filename
}
