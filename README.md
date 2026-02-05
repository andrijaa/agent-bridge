## How to Run

### Run Server(SFU on Pion)
```
go work sync
go run server/*
```

### Run Agent
```
go run examples/ai_agent/main.go -id agent1 -room test -test-audio=false -assemblyai-key xxxxx -openai-key xxxx -elevenlabs-key xxxxx
```

* NOTE: you can also use -deepgram-key as well :-) 

### Run Web UI

```
npm install
npm run dev
```
- open http://localhost:3000/ and start an agent
