#!/usr/bin/env python3
"""全引脚障碍布线器:把元件全部引脚(含备用)+ 现有线 + 异网旗都纳入障碍,
为指定 pin 找 direction+offset 使 stub+flag 不碰任何异网 pin/线/旗。
用法: route_pins.py <dryrun.txt> <allgeom.json> <project> <pin1,pin2,...>
"""
import json, subprocess, sys
sys.path.insert(0,'/private/tmp/claude-503/-Users-mikas-github-easyeda-agent/ed4f8897-a4d9-4188-96c9-710ba6dda456/scratchpad')
from placer import build_plan, geom, sr, st, ORDER

def main():
    dry, geomf, project, pinlist = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4].split(',')
    geo=json.load(open(geomf))
    plans={p['pin']:p for p in build_plan(dry)}
    allpins=[tuple(p) for p in geo['pins']]
    segs=[]
    for pts in geo['segs']:
        for i in range(len(pts)-1): segs.append((pts[i][0],pts[i][1],pts[i+1][0],pts[i+1][1]))
    anchors=[]
    for s in segs:
        anchors.append((s[0]-6,s[1]-6,s[0]+6,s[1]+6)); anchors.append((s[2]-6,s[3]-6,s[2]+6,s[3]+6))
    flag_rects=[(f['x1'],f['y1'],f['x2'],f['y2'],f['net']) for f in geo['flags']]
    accepted=[]
    def clear(p,d,off,net):
        r,seg=geom(p,d,off)
        for s in segs:
            if st(seg,s): return None
        for ab in anchors:
            if sr(seg,ab): return None
        for (px,py) in allpins:
            if abs(px-p['px'])<0.5 and abs(py-p['py'])<0.5: continue
            if sr(seg,(px-4,py-4,px+4,py+4)): return None
        for (x1,y1,x2,y2,n2) in flag_rects:
            if n2==net: continue
            if sr(seg,(x1,y1,x2,y2)): return None
        for (seg2,n2) in accepted:
            if st(seg,seg2): return None
            if sr(seg,(seg2[2]-6,seg2[3]-6,seg2[2]+6,seg2[3]+6)): return None
        return seg
    done=0; fail=[]
    for pin in pinlist:
        p=plans.get(pin)
        if not p: fail.append(pin+'(no-plan)'); continue
        net=p['net']; picked=None
        for d in ORDER[p['dir']]:
            for off in range(18,300,6):
                seg=clear(p,d,off,net)
                if seg: picked=(d,off,seg); break
            if picked: break
        if not picked: fail.append(pin); continue
        d,off,seg=picked
        r=subprocess.run(['easyeda','sch','connect','--x',str(p['px']),'--y',str(p['py']),'--kind',p['kind'],'--net',net,'--direction',d,'--offset',str(off),'--project',project],capture_output=True,text=True,timeout=90)
        if '"ok": true' in r.stdout: accepted.append((seg,net)); done+=1; print('%-10s %s off=%d ok'%(pin,d,off))
        else: fail.append(pin+'(draw)'); print('%-10s DRAW-FAIL'%pin)
    print('落笔 %d/%d 未解 %s'%(done,len(pinlist),fail))

if __name__=='__main__': main()
