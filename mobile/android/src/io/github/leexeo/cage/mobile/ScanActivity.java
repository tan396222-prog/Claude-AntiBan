package io.github.leexeo.cage.mobile;

import android.animation.ValueAnimator;
import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.*;
import android.hardware.Camera;
import android.os.Bundle;
import android.view.*;
import android.view.animation.LinearInterpolator;
import android.widget.*;
import com.google.zxing.*;
import com.google.zxing.common.HybridBinarizer;
import com.google.zxing.qrcode.QRCodeReader;

// The camera only runs on this screen. Preview frames are decoded on-device.
@SuppressWarnings("deprecation")
public final class ScanActivity extends Activity implements SurfaceHolder.Callback, Camera.PreviewCallback {
    private Camera camera;
    private SurfaceView preview;
    private TextView message;
    private Finder finder;
    private final QRCodeReader reader = new QRCodeReader();
    private boolean resumed, finished;
    private static final int PERMISSION = 1;
    private int dp(float n) { return Math.round(n * getResources().getDisplayMetrics().density); }

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        getWindow().getDecorView().setSystemUiVisibility(0);
        getWindow().setStatusBarColor(0xff141716); getWindow().setNavigationBarColor(0xff141716);
        FrameLayout frame = new FrameLayout(this); frame.setBackgroundColor(0xff141716);
        preview = new SurfaceView(this);
        frame.addView(preview,new FrameLayout.LayoutParams(-1,-1,Gravity.CENTER));
        preview.getHolder().addCallback(this);
        finder = new Finder(); frame.addView(finder,new FrameLayout.LayoutParams(-1,-1));
        LinearLayout bar = new LinearLayout(this);bar.setGravity(Gravity.CENTER_VERTICAL);bar.setPadding(dp(12),0,dp(20),0);
        Button cancel = new Button(this);cancel.setText("取消");cancel.setTextColor(Color.WHITE);cancel.setTextSize(15);
        cancel.setBackgroundColor(Color.TRANSPARENT);cancel.setOnClickListener(v -> finish());bar.addView(cancel,new LinearLayout.LayoutParams(dp(72),dp(56)));
        TextView title = new TextView(this);title.setText("扫描电脑二维码");title.setTextColor(Color.WHITE);title.setTextSize(18);title.setTypeface(Typeface.DEFAULT,Typeface.BOLD);
        bar.addView(title); frame.addView(bar,new FrameLayout.LayoutParams(-1,dp(64),Gravity.TOP));
        LinearLayout instructions = new LinearLayout(this);instructions.setOrientation(LinearLayout.VERTICAL);instructions.setGravity(Gravity.CENTER);
        instructions.setPadding(dp(24),dp(12),dp(24),dp(20));
        TextView heading = new TextView(this);heading.setText("连接你的电脑");heading.setTextSize(22);heading.setTextColor(Color.WHITE);heading.setTypeface(Typeface.DEFAULT,Typeface.BOLD);instructions.addView(heading);
        message = new TextView(this);message.setGravity(Gravity.CENTER);message.setTextSize(15);message.setPadding(0,dp(12),0,dp(10));
        message.setTextColor(0xffe1e5e2);message.setText("将 Cage 配对二维码放入框内\n在电脑「设置 → 远程连接」中生成");
        message.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);instructions.addView(message);
        TextView privacy = new TextView(this);privacy.setText("仅识别 Cage 配对码");privacy.setTextSize(12);privacy.setTextColor(0xffa9b4ae);instructions.addView(privacy);
        FrameLayout.LayoutParams copy = new FrameLayout.LayoutParams(-1,-2,Gravity.BOTTOM);frame.addView(instructions,copy);
        frame.setOnApplyWindowInsetsListener((view,insets) -> {
            view.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());return insets;
        });
        setContentView(frame);
        if(checkSelfPermission(android.Manifest.permission.CAMERA)!=PackageManager.PERMISSION_GRANTED)
            requestPermissions(new String[]{android.Manifest.permission.CAMERA},PERMISSION);
    }
    @Override public void onRequestPermissionsResult(int id,String[] names,int[] results) {
        super.onRequestPermissionsResult(id,names,results);
        if(id!=PERMISSION)return;
        if(results.length>0 && results[0]==PackageManager.PERMISSION_GRANTED) start();
        else finishWithError("相机权限未开启。可返回使用配对码，或在系统设置中允许相机访问。");
    }
    private void finishWithError(String error) {setResult(RESULT_CANCELED,new Intent().putExtra("error",error));finish();}
    private void start() {
        if(!resumed || isFinishing() || camera!=null || !preview.getHolder().getSurface().isValid() ||
            checkSelfPermission(android.Manifest.permission.CAMERA)!=PackageManager.PERMISSION_GRANTED) return;
        try {
            camera=Camera.open();
            Camera.Parameters p=camera.getParameters();
            if(p.getSupportedFocusModes()!=null && p.getSupportedFocusModes().contains(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE)) {
                p.setFocusMode(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE);camera.setParameters(p);
            }
            Camera.CameraInfo info=new Camera.CameraInfo();Camera.getCameraInfo(0,info);
            int rotation=(info.orientation-getWindowManager().getDefaultDisplay().getRotation()*90+360)%360;
            camera.setDisplayOrientation(rotation);
            // Keep the camera image in proportion; the parent clips the overflow.
            Camera.Size size=p.getPreviewSize();
            preview.post(() -> {
                int width=(rotation%180==0)?size.width:size.height, height=(rotation%180==0)?size.height:size.width;
                View parent=(View)preview.getParent();
                float scale=Math.max((float)parent.getWidth()/width,(float)parent.getHeight()/height);
                preview.setLayoutParams(new FrameLayout.LayoutParams(Math.round(width*scale),Math.round(height*scale),Gravity.CENTER));
            });
            camera.setPreviewDisplay(preview.getHolder());camera.setPreviewCallback(this);camera.startPreview();finder.animateScan(true);
        } catch(Exception e){stop();finishWithError("无法打开相机，请重试或返回使用配对码。");}
    }
    private void stop(){finder.animateScan(false);if(camera!=null){camera.setPreviewCallback(null);camera.release();camera=null;}}
    @Override public void onPreviewFrame(byte[] data,Camera source){
        if(data==null || isFinishing() || finished)return;
        Camera.Size size=source.getParameters().getPreviewSize();
        try{
            PlanarYUVLuminanceSource pixels=new PlanarYUVLuminanceSource(data,size.width,size.height,0,0,size.width,size.height,false);
            String code=reader.decode(new BinaryBitmap(new HybridBinarizer(pixels))).getText();
            if(code.startsWith("CAGE1:") && code.length()<=4096){
                finished=true;finder.performHapticFeedback(HapticFeedbackConstants.CONFIRM);
                setResult(RESULT_OK,new Intent().putExtra("code",code));finish();
            } else message.setText("请扫描 Cage 配对二维码\n在电脑「设置 → 远程连接」中生成");
        } catch(ReaderException ignored){} finally {reader.reset();}
    }
    @Override public void onResume(){super.onResume();resumed=true;start();}
    @Override public void onPause(){resumed=false;stop();super.onPause();}
    public void surfaceCreated(SurfaceHolder h){start();}
    public void surfaceChanged(SurfaceHolder h,int f,int w,int height){start();}
    public void surfaceDestroyed(SurfaceHolder h){stop();}

    private final class Finder extends View {
        private final Paint paint=new Paint(Paint.ANTI_ALIAS_FLAG);
        private final ValueAnimator sweep=ValueAnimator.ofFloat(0,1);
        private float progress=.5f;
        Finder(){
            super(ScanActivity.this);setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
            sweep.setDuration(2400);sweep.setRepeatCount(ValueAnimator.INFINITE);sweep.setRepeatMode(ValueAnimator.REVERSE);sweep.setInterpolator(new LinearInterpolator());
            sweep.addUpdateListener(value -> {progress=(float)value.getAnimatedValue();invalidate();});
        }
        void animateScan(boolean running){
            if(running && ValueAnimator.areAnimatorsEnabled()){if(!sweep.isStarted())sweep.start();}
            else {sweep.cancel();progress=.5f;invalidate();}
        }
        @Override protected void onDraw(Canvas canvas){
            float side=Math.max(dp(96),Math.min(dp(280),Math.min(getWidth()-dp(56),getHeight()-dp(250))));
            float left=(getWidth()-side)/2, top=dp(72)+Math.max(0,(getHeight()-dp(250)-side)/2);
            RectF box=new RectF(left,top,left+side,top+side);
            Path shade=new Path();shade.setFillType(Path.FillType.EVEN_ODD);
            shade.addRect(0,0,getWidth(),getHeight(),Path.Direction.CW);shade.addRoundRect(box,dp(18),dp(18),Path.Direction.CW);
            paint.setStyle(Paint.Style.FILL);paint.setColor(0xb8141716);canvas.drawPath(shade,paint);
            paint.setStyle(Paint.Style.STROKE);paint.setStrokeWidth(dp(1));paint.setColor(0x70ffffff);canvas.drawRoundRect(box,dp(18),dp(18),paint);
            paint.setColor(0xffe8b79d);paint.setStrokeWidth(dp(4));paint.setStrokeCap(Paint.Cap.ROUND);
            for(int x=0;x<2;x++)for(int y=0;y<2;y++){
                canvas.save();canvas.translate(x==0?box.left:box.right,y==0?box.top:box.bottom);canvas.scale(x==0?1:-1,y==0?1:-1);
                Path corner=new Path();corner.moveTo(0,dp(30));corner.lineTo(0,dp(18));corner.quadTo(0,0,dp(18),0);corner.lineTo(dp(30),0);canvas.drawPath(corner,paint);canvas.restore();
            }
            canvas.save();canvas.clipRect(box);float line=box.top+dp(24)+(side-dp(48))*progress;
            paint.setStyle(Paint.Style.FILL);paint.setShader(new LinearGradient(0,line-dp(28),0,line,0x00e8b79d,0x35e8b79d,Shader.TileMode.CLAMP));
            canvas.drawRect(box.left+dp(10),line-dp(28),box.right-dp(10),line,paint);paint.setShader(null);
            paint.setColor(0xffe8b79d);canvas.drawRoundRect(box.left+dp(12),line,box.right-dp(12),line+dp(2),dp(1),dp(1),paint);canvas.restore();
        }
    }
}
