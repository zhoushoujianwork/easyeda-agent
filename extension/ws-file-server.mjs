import { WebSocketServer } from 'ws';
import { readFileSync } from 'fs';
const wss = new WebSocketServer({ port: 49777 });
wss.on('connection', ws => {
  ws.on('message', msg => {
    try {
      const req = JSON.parse(msg.toString());
      if (req.action === 'getFile') {
        const bytes = readFileSync('/Users/mikas/github/easyeda-agent/extension/dist/index.js');
        ws.send(JSON.stringify({ content: bytes.toString('base64') }));
      }
    } catch(e){ ws.send(JSON.stringify({error:String(e)})); }
  });
});
console.log('ws file server :49777');
