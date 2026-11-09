import com.huawei.hosscrcpy.api.HosRemoteConfig;
import com.huawei.hosscrcpy.api.HosRemoteDevice;
import com.huawei.hosscrcpy.api.ScreenCapCallback;
import java.io.BufferedOutputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicReference;

// stdout is a binary pipe: a big-endian length followed by one SDK buffer.
public final class PinkyH264 {
    public static void main(String[] args) throws Exception {
        var video = new DataOutputStream(new BufferedOutputStream(System.out));
        System.setOut(System.err);
        var done = new CountDownLatch(1);
        var error = new AtomicReference<Throwable>();
        var config = new HosRemoteConfig(args[0]);
        config.setHdcPath(args[1]);
        config.setScale(Integer.parseInt(args[2]));
        config.setFrameRate(Integer.parseInt(args[3]));
        config.setBitRate(8);
        config.setIFrameInterval(1000);
        config.setRepeatInterval(0);
        var device = new HosRemoteDevice(config);
        var stop = new Thread(() -> {
            try {
                int command;
                while ((command = System.in.read()) != -1 && done.getCount() != 0) {
                    switch (command) {
                        case 'H' -> device.setRotationHorizontal();
                        case 'V' -> device.setRotationVertical();
                        default -> { }
                    }
                }
            } catch (IOException ignored) {
            } finally {
                done.countDown();
            }
        });
        stop.setDaemon(true);
        stop.start();
        try {
            device.startCaptureScreen(new ScreenCapCallback() {
                public void onReady() { System.err.println("HOScrcpy video connected"); }
                public void onException(Throwable cause) {
                    error.compareAndSet(null, cause);
                    done.countDown();
                }
                public void onData(ByteBuffer buffer) {
                    var data = new byte[buffer.remaining()];
                    if (data.length == 0 || done.getCount() == 0) return;
                    buffer.duplicate().get(data);
                    synchronized (video) {
                        try {
                            video.writeInt(data.length);
                            video.write(data);
                            video.flush();
                        } catch (IOException cause) {
                            onException(cause);
                        }
                    }
                }
            });
            done.await();
        } finally {
            device.stopCaptureScreen();
        }
        if (error.get() != null) {
            error.get().printStackTrace(System.err);
            System.exit(1);
        }
        System.exit(0);
    }
}
