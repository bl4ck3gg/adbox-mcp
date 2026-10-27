package mr.robot.agent.scrcpy.control;

public final class DeviceMessage {

    public static final int TYPE_CLIPBOARD = 0;
    public static final int TYPE_ACK_CLIPBOARD = 1;
    public static final int TYPE_UHID_OUTPUT = 2;
    /** 物理屏 rotation/size 变更时上行，供主机经 DataChannel 通知网页端 */
    public static final int TYPE_DISPLAY_GEOMETRY = 3;

    private int type;
    private String text;
    private long sequence;
    private int id;
    private byte[] data;
    private int displayRotation;
    private int displayWidth;
    private int displayHeight;

    private DeviceMessage() {
    }

    public static DeviceMessage createClipboard(String text) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_CLIPBOARD;
        event.text = text;
        return event;
    }

    public static DeviceMessage createAckClipboard(long sequence) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_ACK_CLIPBOARD;
        event.sequence = sequence;
        return event;
    }

    public static DeviceMessage createUhidOutput(int id, byte[] data) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_UHID_OUTPUT;
        event.id = id;
        event.data = data;
        return event;
    }

    public static DeviceMessage createDisplayGeometry(int rotation, int width, int height) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_DISPLAY_GEOMETRY;
        event.displayRotation = rotation;
        event.displayWidth = width;
        event.displayHeight = height;
        return event;
    }

    public int getType() {
        return type;
    }

    public String getText() {
        return text;
    }

    public long getSequence() {
        return sequence;
    }

    public int getId() {
        return id;
    }

    public byte[] getData() {
        return data;
    }

    public int getDisplayRotation() {
        return displayRotation;
    }

    public int getDisplayWidth() {
        return displayWidth;
    }

    public int getDisplayHeight() {
        return displayHeight;
    }
}
