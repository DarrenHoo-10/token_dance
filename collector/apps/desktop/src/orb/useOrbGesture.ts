import { useEffect, useRef, useState, type MouseEvent, type PointerEvent } from 'react';
import { orbAction, orbBeginDrag, orbEndDrag, orbFling, orbMove } from './bridge';
import { DRAG_THRESHOLD_DIP } from './types';

const POWER_UNIT_DIP = 160;
type Gesture = { id: number; button: number; x: number; y: number; dragging: boolean; pending: {dx:number;dy:number} | null; draining: boolean };

export function useOrbGesture(hidden: boolean) {
  const current = useRef<Gesture | null>(null);
  const validClicks = useRef(0);
  const tail = useRef(Promise.resolve());
  const [charging, setCharging] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [pull, setPull] = useState({ power: 0, angle: 0 });
  const [error, setError] = useState('');
  const enqueue = (work: () => Promise<void>) => {
    tail.current = tail.current.then(work).catch(err => setError(String(err)));
  };
  const flushMoves = async (gesture: Gesture) => {
    try {
      while (gesture.pending) {
        const {dx,dy}=gesture.pending; gesture.pending=null;
        await orbMove(dx,dy);
      }
    } finally { gesture.draining=false; }
  };
  const cancel = () => {
    const gesture = current.current;
    if (gesture) validClicks.current=0;
    current.current=null;
    setCharging(false);setDragging(false);
    setPull({power:0,angle:0});
    if (gesture?.button===2) enqueue(()=>orbAction('cancel_pull'));
    else if (gesture?.dragging) enqueue(async () => {await flushMoves(gesture);await orbEndDrag();});
    else if (gesture?.button===0) enqueue(()=>orbAction('release_grab'));
  };
  useEffect(() => {
    window.addEventListener('blur', cancel);
    const onVisibility = () => {if(document.hidden)cancel();};
    document.addEventListener('visibilitychange',onVisibility);
    return () => { window.removeEventListener('blur',cancel);document.removeEventListener('visibilitychange',onVisibility);cancel(); };
  }, []);
  useEffect(() => {if(hidden)cancel();}, [hidden]);

  const onPointerDown = (event: PointerEvent<HTMLButtonElement>) => {
    if (![0,2].includes(event.button) || current.current) return;
    event.preventDefault();
    if(event.button!==0)validClicks.current=0;
    setError('');
    event.currentTarget.setPointerCapture(event.pointerId);
    current.current={id:event.pointerId,button:event.button,x:event.screenX,y:event.screenY,dragging:event.button===2,pending:null,draining:false};
    setCharging(event.button===2);
    setPull({power:0,angle:0});
    enqueue(()=>orbAction(event.button===2?'begin_pull':'grab'));
  };
  const onPointerMove = (event: PointerEvent<HTMLButtonElement>) => {
    const gesture=current.current;
    if (!gesture || gesture.id!==event.pointerId) return;
    const dx=event.screenX-gesture.x,dy=event.screenY-gesture.y;
    if (gesture.button===2) setPull({power:Math.hypot(dx,dy)/POWER_UNIT_DIP,angle:Math.atan2(-dy,-dx)*180/Math.PI});
    if (!gesture.dragging && Math.hypot(dx,dy)>DRAG_THRESHOLD_DIP) {
      validClicks.current=0;
      gesture.dragging=true;setDragging(true);enqueue(orbBeginDrag);
    }
    if (!gesture.dragging) return;
    gesture.pending={dx,dy};
    if (!gesture.draining) {gesture.draining=true;enqueue(()=>flushMoves(gesture));}
  };
  const onPointerUp = (event: PointerEvent<HTMLButtonElement>) => {
    const gesture=current.current;
    if (!gesture || gesture.id!==event.pointerId || gesture.button!==event.button) return;
    current.current=null;setCharging(false);setDragging(false);setPull({power:0,angle:0});
    const dx=event.screenX-gesture.x,dy=event.screenY-gesture.y;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if(gesture.button===2) {
      gesture.pending={dx,dy};
      enqueue(async()=>{
        await flushMoves(gesture);
        if(Math.hypot(dx,dy)<=DRAG_THRESHOLD_DIP) await orbAction('cancel_pull');
        else await orbFling(dx,dy);
      });
    }
    else if(gesture.dragging) {gesture.pending={dx,dy};enqueue(async()=>{await flushMoves(gesture);await orbEndDrag();});}
    else {
      validClicks.current=Math.min(2,validClicks.current+1);
      enqueue(()=>orbAction('reveal'));
    }
  };
  const onDoubleClick = (event: MouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    // Use the system double-click gesture, but do not count a drag or a
    // cancelled press as one of its two clicks.
    if(event.button!==0 || current.current || validClicks.current<2)return;
    validClicks.current=0;
    enqueue(()=>orbAction('activate'));
  };
  return {charging,dragging,pull,error,onPointerDown,onPointerMove,onPointerUp,onDoubleClick,onPointerCancel:cancel,onLostPointerCapture:cancel};
}
