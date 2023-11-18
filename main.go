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
		startTimer(timer)
		toggleRecording()
	})

	button2 := widget.NewButton("Stop recording", func() {
		stopTimer()
		stopRecording()
		response := apiCall()
		err := json.Unmarshal([]byte(response), &data)
		if err != nil {
			log.Println(err)
		}
		str.Set(data.Text)
		clipboardWrite(data.Text)
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

func apiCall() string {
	audioFilePath := "/tmp/output.wav"
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
	return string(responseBody)
}

func recordAudio(stopChan <-chan struct{}) {
	log.Println("Recording audio...")
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

	filename := "/tmp/output.wav"

	f, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}

	stream.Start()

	for {
		select {
		case <-stopChan:
			log.Println("Stop recording")
			stream.Stop()
			return
		default:
			// continue recording
		}

		err = wav.Encode(f, stream, format)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func toggleRecording() {
	if recording {
		stopRecording()
	} else {
		startRecording()
	}
	recording = !recording
}

func startRecording() {
	log.Println("start recording")
	go recordAudio(stopChan)
	stopChan = make(chan struct{})
}

func stopRecording() {
	log.Println("stop recording")
	if recording {
		log.Println("not recording")
	} else {
		close(stopChan)
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
