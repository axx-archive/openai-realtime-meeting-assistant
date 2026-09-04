package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// AJ 2026-09-03, 13 seconds into a meeting on a 114 ms link: "i'm in the room,
// why do I have poor connection?". The badge read "poor · bandwidth · 114ms".
func TestLocalSeatQualityGradeNeedsSustainedEvidenceNotOneEncoderHint(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`const localUplinkSustainedSamples = 3`,
		`const sustained = localUplinkLimitedSamples >= localUplinkSustainedSamples && !mediaQualityBandwidthLimited`,
		// the number shown must belong to the reason given
		"label = `poor · latency · ${rttMs}ms`",
		"label = `poor · uplink · ${Math.round(uplinkBitrate / 1000)}kbps`",
		// simulcast: the reason comes from the layer carrying the stream
		`if (stat.qualityLimitationReason && layerTargetBitrate >= outboundVideoReasonTarget) {`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("local seat quality contract missing %q", want)
		}
	}
	// The old form printed "poor" off a single 'bandwidth' sample. Pin its
	// absence: this exact expression is what AJ saw.
	if strings.Contains(html, `if (reason === 'bandwidth' || rttMs >= 450) level = 'poor'`) {
		t.Fatal("a single bandwidth-limited sample must no longer grade the local seat poor")
	}
}

func TestRenderedLocalSeatQualityBadgeIsHonestAboutAHealthyLink(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered browser contract")
	}
	indexPath, err := filepath.Abs("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs');const http=require('http');const path=require('path');const assert=require('assert/strict');const {chromium}=require('playwright');
const idx=process.env.QUALITY_INDEX;const html=fs.readFileSync(idx,'utf8');
const dictation=fs.readFileSync(path.join(path.dirname(idx),'public','composer-dictation.js'),'utf8');
const server=http.createServer((req,res)=>{
 if(req.url==='/public/composer-dictation.js'){res.writeHead(200,{'content-type':'application/javascript'});return res.end(dictation)}
 if(req.url==='/auth/me'){res.writeHead(200,{'content-type':'application/json'});return res.end(JSON.stringify({email:'aj@shareability.com',name:'AJ',shellAccess:'full',organization:'Bonfire'}))}
 if(req.url.startsWith('/assistant/')||req.url.startsWith('/api/')||req.url.startsWith('/rooms')||req.url.startsWith('/notifications')||req.url.startsWith('/auth/theme')){res.writeHead(200,{'content-type':'application/json'});return res.end('{}')}
 res.writeHead(200,{'content-type':'text/html; charset=utf-8'});res.end(html)
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));
const b=await chromium.launch({headless:true});const page=await b.newPage({viewport:{width:1280,height:800}});
await page.goto('http://127.0.0.1:'+server.address().port+'/',{waitUntil:'domcontentloaded'});
await page.waitForSelector('#appShell.is-authed');
const grade=await page.evaluate(()=>{
 const snap=(over={})=>Object.assign({at:0,inboundVideoByParticipant:{},outboundVideoQualityLimitationReason:'',outboundRtt:0,candidatePair:{availableOutgoingBitrate:900000}},over);
 const run=(s,prev)=>{try{renderTileQualityBadges(s,prev)}catch(e){/* the DOM loop needs a room; the map is set before it */}
  return tileQualityByName.get('aj')?.label||''};
 currentParticipantName='AJ';tileQualityByName=new Map();mediaQualityBandwidthLimited=false;
 const out={};
 // AJ's exact case: first sample of a fresh sitting, bandwidth-limited while
 // the estimate ramps, on a healthy 114 ms link
 const bw=snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114});
 out.firstSample=run(bw,null);
 out.secondSample=run(snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114}),bw);
 out.thirdSample=run(snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114}),bw);
 out.sustained=run(snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114}),bw);
 // our own cap must never be reported as a network fault
 mediaQualityBandwidthLimited=true;
 out.selfCapped=run(snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114}),bw);
 mediaQualityBandwidthLimited=false;
 // a genuinely bad uplink still escalates
 tileQualityByName=new Map();
 let prev=null;for(let i=0;i<4;i++){const s=snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114,candidatePair:{availableOutgoingBitrate:200000}});out.starvedUplink=run(s,prev);prev=s}
 // real latency is poor on the first sample, with no streak required
 tileQualityByName=new Map();
 out.latency=run(snap({outboundRtt:0.5}),null);
 // a rejoin cannot inherit the streak
 tileQualityByName=new Map();
 out.afterRejoin=run(snap({outboundVideoQualityLimitationReason:'bandwidth',outboundRtt:0.114}),null);
 return out});
assert.equal(grade.firstSample,'','a 114ms link must not read poor on one bandwidth sample: '+JSON.stringify(grade));
assert.equal(grade.secondSample,'','two samples is still not sustained: '+JSON.stringify(grade));
assert.equal(grade.thirdSample,'fair · uplink · 900kbps','three consecutive samples should read fair, not poor: '+JSON.stringify(grade));
assert.equal(grade.sustained,'fair · uplink · 900kbps','a capped-but-healthy uplink must never escalate past fair: '+JSON.stringify(grade));
assert.equal(grade.selfCapped,'','our own adaptation must not be reported as a network fault: '+JSON.stringify(grade));
assert.equal(grade.starvedUplink,'poor · uplink · 200kbps','a genuinely starved uplink must still read poor: '+JSON.stringify(grade));
assert.equal(grade.latency,'poor · latency · 500ms','real latency must grade immediately: '+JSON.stringify(grade));
assert.equal(grade.afterRejoin,'','a rejoin must not inherit the previous streak: '+JSON.stringify(grade));
// simulcast: the reason belongs to the layer carrying the stream
const reason=await page.evaluate(()=>{
 const stats=[{type:'outbound-rtp',kind:'video',bytesSent:1,framesEncoded:1,framesSent:1,targetBitrate:180000,qualityLimitationReason:'bandwidth'},
              {type:'outbound-rtp',kind:'video',bytesSent:9,framesEncoded:9,framesSent:9,targetBitrate:1200000,qualityLimitationReason:'none'}];
 const report={forEach:cb=>stats.forEach(cb),get:()=>null};
 const a=summarizeMediaQualityStats(report).outboundVideoQualityLimitationReason;
 const report2={forEach:cb=>[...stats].reverse().forEach(cb),get:()=>null};
 const bb=summarizeMediaQualityStats(report2).outboundVideoQualityLimitationReason;
 return {a,b:bb}});
assert.equal(reason.a,'none','the top simulcast layer owns the reason: '+JSON.stringify(reason));
assert.equal(reason.b,reason.a,'the reason must not depend on stat iteration order: '+JSON.stringify(reason));
await b.close();server.close()})().catch(e=>{console.error(e);server.close();process.exit(1)});`
	cmd := exec.Command("node", "-e", script)
	cmd.Env = append(os.Environ(), "QUALITY_INDEX="+indexPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered local seat quality badge failed: %v\n%s", err, output)
	}
}
