package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIPadWebKitCountryGolfSizedChannelSettlesWithoutLayoutShake(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered WebKit stability contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs = require('fs');
const http = require('http');
const assert = require('assert/strict');
const { webkit } = require('playwright');
const html = fs.readFileSync(process.env.IPAD_LONG_CHANNEL_INDEX, 'utf8');
const server = http.createServer((req, res) => {
  if (req.url === '/public/composer-dictation.js') {
    res.writeHead(200, {'content-type':'application/javascript'});
    return res.end('');
  }
  if (req.url === '/auth/me') {
    res.writeHead(200, {'content-type':'application/json'});
    return res.end(JSON.stringify({email:'synthetic@example.test',name:'Synthetic',shellAccess:'full'}));
  }
  if (req.url === '/api/stride/v1/mobile/surfaces/organizations') {
    res.writeHead(200, {'content-type':'application/json'});
    return res.end(JSON.stringify({availability:'available',surface:'organizations',revision:1,items:[]}));
  }
  if (req.url.startsWith('/api/') || req.url.startsWith('/assistant/') || req.url.startsWith('/notifications') || req.url.startsWith('/rooms') || req.url.startsWith('/artifacts')) {
    res.writeHead(404, {'content-type':'application/json'});
    return res.end('{}');
  }
  res.writeHead(200, {'content-type':'text/html; charset=utf-8'});
  res.end(html);
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = 'http://127.0.0.1:' + server.address().port;
  const browser = await webkit.launch({headless:true});
  const context = await browser.newContext({
    viewport:{width:1024,height:768},
    hasTouch:true,
    isMobile:true,
    userAgent:'Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1',
  });
  const page = await context.newPage();
  await page.addInitScript(() => {
    const NativeResizeObserver = window.ResizeObserver;
    window.__resizeObserverEvidence = {callbacks:0,targets:[]};
    window.ResizeObserver = class ResizeObserver {
      constructor(callback) {
        this._observer = new NativeResizeObserver(entries => {
          window.__resizeObserverEvidence.callbacks += 1;
          callback(entries, this);
        });
      }
      observe(target, options) {
        window.__resizeObserverEvidence.targets.push({id:target.id || '',className:String(target.className || '')});
        this._observer.observe(target, options);
      }
      unobserve(target) { this._observer.unobserve(target); }
      disconnect() { this._observer.disconnect(); }
      takeRecords() { return this._observer.takeRecords(); }
    };
  });
  await page.goto(base + '/chat', {waitUntil:'domcontentloaded'});
  await page.waitForSelector('#appShell.is-authed');
  await page.waitForFunction(() => document.getElementById('appShell').dataset.tool === 'chat');

  // Mirrors the live Country Golf size profile without reading or copying any
  // production body: 37 turns, one 85,184-character user message, and four
  // additional long replies.
  await page.evaluate(() => {
    const lengths = [85184, 2117, 1344, 1227, 938];
    const messages = Array.from({length:37}, (_, index) => {
      const length = lengths[index] || 180 + (index % 5) * 31;
      const role = index === 0 ? 'user' : index < 5 ? 'scout' : index % 3 === 0 ? 'user' : 'scout';
      return {
        id:'country-golf-sized-' + index,
        role,
        text:('Country golf layout stability sentence ' + index + '. ').repeat(Math.ceil(length / 43)).slice(0, length),
        createdAt:'2026-08-15T19:' + String(index).padStart(2, '0') + ':00Z',
      };
    });
    scoutChatThreads=[{id:'country-golf-sized',title:'Country Golf',visibility:'public',messages}];
    activeScoutThreadId='country-golf-sized';
    const thread=document.getElementById('scoutChatThread');
    thread.replaceChildren(...messages.map(message => {
      const node=scoutChatMessageNode(message.role === 'user' ? 'user' : 'scout', message.text, message.createdAt, [], message.role === 'user' ? 'Synthetic' : 'Scout', false);
      node.dataset.messageId=message.id;
      return node;
    }));
    thread.scrollTop=120;
  });
  await page.waitForFunction(() => document.querySelectorAll('#scoutChatThread .scout-chat-msg__expand').length >= 5);
  await page.waitForTimeout(250);

  const sampleFrames = () => page.evaluate(async () => {
    const thread=document.getElementById('scoutChatThread');
    const longItem=thread.querySelector('[data-message-id="country-golf-sized-0"]');
    const samples=[];
    for (let index=0; index<24; index += 1) {
      await new Promise(resolve => requestAnimationFrame(resolve));
      samples.push({
        threadHeight:Math.round(thread.getBoundingClientRect().height * 100) / 100,
        scrollHeight:thread.scrollHeight,
        scrollTop:thread.scrollTop,
        longItemHeight:Math.round(longItem.getBoundingClientRect().height * 100) / 100,
        clamped:longItem.querySelector('.scout-chat-text').classList.contains('is-clamped'),
        controls:thread.querySelectorAll('.scout-chat-msg__expand').length,
      });
    }
    return samples;
  });
  const assertSettled = samples => {
    const states = new Set(samples.map(sample => JSON.stringify(sample)));
    assert.equal(states.size, 1, 'WebKit geometry changed after settling: ' + JSON.stringify(samples));
    assert.equal(samples[0].clamped, true);
    assert.ok(samples[0].controls >= 5);
  };
  assertSettled(await sampleFrames());

  await page.setViewportSize({width:980,height:768});
  await page.waitForTimeout(150);
  await page.setViewportSize({width:1024,height:768});
  await page.waitForTimeout(250);
  assertSettled(await sampleFrames());

  const evidence = await page.evaluate(() => window.__resizeObserverEvidence);
  const messageTargets = evidence.targets.filter(target => target.className.includes('scout-chat-msg'));
  assert.deepEqual(messageTargets, [], 'a long message observed its own mutable height');
  assert.ok(evidence.targets.some(target => target.id === 'scoutChatThread'), JSON.stringify(evidence));

  await browser.close();
  server.close();
})().catch(error => {
  console.error(error);
  server.close();
  process.exit(1);
});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "IPAD_LONG_CHANNEL_INDEX="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered iPad WebKit long-channel harness: %v\n%s", err, output)
	}
}
