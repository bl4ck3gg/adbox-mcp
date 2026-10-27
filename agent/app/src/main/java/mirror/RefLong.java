package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefLong {
    private Field field;

    public RefLong(Class cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public long get(Object object) {
        try {
            return this.field.getLong(object);
        } catch (Exception e) {
            return 0L;
        }
    }

    public void set(Object obj, long value) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.setLong(obj, value);
        } catch (Exception e) {
        }
    }
}
