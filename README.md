## How to Run

1. Run Server(SFU on Pion)
go work sync
go run server/*

2. Run Agent
go run examples/ai_agent/main.go -id agent1 -room test -test-audio=false -assemblyai-key xxxxx -openai-key xxxx -elevenlabs-key xxxxx

NOTE: you can also use -deepgram-key as well :-) 

3. Run Web UI
npm install
npm run dev
open http://localhost:3000/ and start an agent